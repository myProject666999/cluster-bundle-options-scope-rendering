// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     https://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package patchtmpl

import (
	"strings"
	"testing"

	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/converter"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/options"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/testutil"
)

// scopedPatchComponent patches the namespace of a Pod from options.
const scopedPatchComponent = `kind: Component
spec:
  componentName: etcd
  version: 1.2.3
  objects:
  - apiVersion: v1
    kind: Pod
    metadata:
      name: etcd-pod
  - kind: PatchTemplate
    template: |
      kind: Pod
      metadata:
        namespace: {{.ns}}
        labels:
          tier: {{.tier}}`

func applyPatch(t *testing.T, compYAML string, opts options.JSONOptions, applier options.Applier) string {
	t.Helper()
	comp, err := converter.FromYAMLString(compYAML).ToComponent()
	if err != nil {
		t.Fatalf("parsing component: %v", err)
	}
	newComp, err := applier.ApplyOptions(comp, opts)
	if err != nil {
		t.Fatalf("ApplyOptions: %v", err)
	}
	out, err := converter.FromObject(newComp).ToYAMLString()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPatchComponentScopedOptions(t *testing.T) {
	testCases := []struct {
		desc         string
		opts         options.JSONOptions
		expSubstrs   []string
		notExpSubstr []string
	}{
		{
			desc: "component-level options override global options",
			opts: options.JSONOptions{
				"ns":   "global-ns",
				"tier": "backend",
				"components": map[string]interface{}{
					"etcd": map[string]interface{}{"ns": "etcd-ns"},
				},
			},
			expSubstrs:   []string{"namespace: etcd-ns", "tier: backend"},
			notExpSubstr: []string{"global-ns"},
		},
		{
			desc: "name@version wins over name",
			opts: options.JSONOptions{
				"ns":   "global-ns",
				"tier": "backend",
				"components": map[string]interface{}{
					"etcd":       map[string]interface{}{"ns": "etcd-ns"},
					"etcd@1.2.3": map[string]interface{}{"ns": "etcd-ns-123"},
				},
			},
			expSubstrs:   []string{"namespace: etcd-ns-123"},
			notExpSubstr: []string{"global-ns"},
		},
		{
			desc: "non-matching component gets global options",
			opts: options.JSONOptions{
				"ns":   "global-ns",
				"tier": "backend",
				"components": map[string]interface{}{
					"other": map[string]interface{}{"ns": "other-ns"},
				},
			},
			expSubstrs:   []string{"namespace: global-ns"},
			notExpSubstr: []string{"other-ns"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			out := applyPatch(t, scopedPatchComponent, tc.opts, NewDefaultApplier())
			for _, want := range tc.expSubstrs {
				if !strings.Contains(out, want) {
					t.Errorf("patched output\n%s\ndoes not contain %q", out, want)
				}
			}
			for _, notWant := range tc.notExpSubstr {
				if strings.Contains(out, notWant) {
					t.Errorf("patched output\n%s\nunexpectedly contains %q", out, notWant)
				}
			}
		})
	}
}

// TestPatchMissingKeyErrorContext verifies that the default missing-key
// error carries the component reference, the template position, and the
// missing key path.
func TestPatchMissingKeyErrorContext(t *testing.T) {
	comp, err := converter.FromYAMLString(`kind: Component
spec:
  componentName: etcd
  version: 1.2.3
  objects:
  - apiVersion: v1
    kind: Pod
    metadata:
      name: etcd-pod
  - kind: PatchTemplate
    template: |
      kind: Pod
      metadata:
        namespace: {{.ns}}`).ToComponent()
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewDefaultApplier().ApplyOptions(comp, options.JSONOptions{})
	if err == nil {
		t.Fatalf("got nil error, want missing-key error")
	}
	for _, want := range []string{"etcd", `key "ns"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}

// TestPatchNoValueResidue verifies the residue check for patch templates
// rendered with a lenient missingkey option.
func TestPatchNoValueResidue(t *testing.T) {
	compYAML := `kind: Component
spec:
  componentName: etcd
  version: 1.2.3
  objects:
  - apiVersion: v1
    kind: Pod
    metadata:
      name: etcd-pod
  - kind: PatchTemplate
    template: |
      kind: Pod
      metadata:
        namespace: {{.ns}}`

	// Error policy: the residue is an error.
	comp, err := converter.FromYAMLString(compYAML).ToComponent()
	if err != nil {
		t.Fatal(err)
	}
	_, err = NewApplierWithConfig(
		WithGoTmplOptions(options.MissingKeyZero),
		WithNoValuePolicy(options.NoValueError),
	).ApplyOptions(comp, options.JSONOptions{})
	if err == nil {
		t.Fatalf("got nil error, want <no value> residue error")
	}
	if !strings.Contains(err.Error(), options.NoValueLiteral) {
		t.Errorf("error %q does not mention the %q literal", err.Error(), options.NoValueLiteral)
	}

	// Warn policy: one warning, rendering succeeds.
	comp, err = converter.FromYAMLString(compYAML).ToComponent()
	if err != nil {
		t.Fatal(err)
	}
	stderr := testutil.CaptureStderr(t, func() {
		_, err = NewApplierWithConfig(
			WithGoTmplOptions(options.MissingKeyZero),
			WithNoValuePolicy(options.NoValueWarn),
		).ApplyOptions(comp, options.JSONOptions{})
	})
	if err != nil {
		t.Errorf("got error %v, want nil", err)
	}
	if got := strings.Count(stderr, "rendered output contains"); got != 1 {
		t.Errorf("got %d warnings (%q), want 1", got, stderr)
	}
}

// TestPatchSchemaDefaultsPriority verifies that schema defaults are filled
// in after option merging and before rendering, and never override values
// the user supplied explicitly.
func TestPatchSchemaDefaultsPriority(t *testing.T) {
	compYAML := `kind: Component
spec:
  componentName: etcd
  version: 1.2.3
  objects:
  - apiVersion: v1
    kind: Pod
    metadata:
      name: etcd-pod
  - kind: PatchTemplate
    optionsSchema:
      properties:
        tier:
          type: string
          default: backend
    template: |
      kind: Pod
      metadata:
        labels:
          tier: {{.tier}}`

	// No user value: the schema default applies.
	out := applyPatch(t, compYAML, options.JSONOptions{}, NewDefaultApplier())
	if !strings.Contains(out, "tier: backend") {
		t.Errorf("expected schema default to be applied, got:\n%s", out)
	}

	// Explicit user value: the default must not override it.
	out = applyPatch(t, compYAML, options.JSONOptions{"tier": "frontend"}, NewDefaultApplier())
	if !strings.Contains(out, "tier: frontend") {
		t.Errorf("expected user value to win over the schema default, got:\n%s", out)
	}
}

// TestPatchSchemaValidationErrorContext verifies that schema validation
// failures keep the field path from the validator and are wrapped with the
// component reference.
func TestPatchSchemaValidationErrorContext(t *testing.T) {
	comp, err := converter.FromYAMLString(`kind: Component
spec:
  componentName: etcd
  version: 1.2.3
  objects:
  - apiVersion: v1
    kind: Pod
    metadata:
      name: etcd-pod
  - kind: PatchTemplate
    optionsSchema:
      required:
      - ns
      properties:
        ns:
          type: string
    template: |
      kind: Pod
      metadata:
        namespace: {{.ns}}`).ToComponent()
	if err != nil {
		t.Fatal(err)
	}

	_, err = NewDefaultApplier().ApplyOptions(comp, options.JSONOptions{})
	if err == nil {
		t.Fatalf("got nil error, want schema validation error")
	}
	// The validator's field path ("ns in body") and the component reference
	// must both be present.
	for _, want := range []string{"ns in body is required", "etcd"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not contain %q", err.Error(), want)
		}
	}
}
