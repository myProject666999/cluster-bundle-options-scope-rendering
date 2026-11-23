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

package gotmpl

import (
	"strings"
	"testing"

	bundle "github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/apis/bundle/v1alpha1"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/converter"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/options"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/testutil"
)

// nestedMissingComponent references a nested key .net.cidr where "net" is
// not provided.
const nestedMissingComponent = `kind: Component
spec:
  componentName: data-component
  version: 1.0.0
  objects:
  - kind: ObjectTemplate
    metadata:
      name: net-pod
    type: go-template
    template: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: net-pod
      spec:
        dnsPolicy: '{{.net.cidr}}'`

func mustComponent(t *testing.T, yaml string) *bundle.Component {
	t.Helper()
	comp, err := converter.FromYAMLString(yaml).ToComponent()
	if err != nil {
		t.Fatalf("parsing component: %v", err)
	}
	return comp
}

func TestNestedMissingKeyDefaultError(t *testing.T) {
	comp := mustComponent(t, nestedMissingComponent)

	// The default (missingkey=error) fails on the missing top-level key and
	// names the key path segment that was missing.
	_, err := NewApplier().ApplyOptions(comp, options.JSONOptions{})
	if err == nil {
		t.Fatalf("got nil error, want missing-key error")
	}
	if !strings.Contains(err.Error(), `map has no entry for key "net"`) {
		t.Errorf("error %q does not name the missing key path", err.Error())
	}
	if !strings.Contains(err.Error(), "data-component") || !strings.Contains(err.Error(), "net-pod") {
		t.Errorf("error %q does not contain the component reference and object name", err.Error())
	}
}

func TestNestedMissingKeyZero(t *testing.T) {
	comp := mustComponent(t, nestedMissingComponent)

	// missingkey=zero yields a nil for the missing top-level key, and
	// accessing a sub-key of nil is itself an execution error.
	_, err := NewApplier(WithGoTmplOptions(options.MissingKeyZero)).ApplyOptions(comp, options.JSONOptions{})
	if err == nil {
		t.Fatalf("got nil error, want nil-pointer error")
	}
	if !strings.Contains(err.Error(), "nil pointer") {
		t.Errorf("error %q is not the expected nil-pointer error", err.Error())
	}
}

func TestNestedMissingKeyDefaultRendersNoValue(t *testing.T) {
	comp := mustComponent(t, nestedMissingComponent)

	// missingkey=default renders the residue literal; the residue check
	// (here: error policy) is what catches it.
	_, err := NewApplier(
		WithGoTmplOptions(options.MissingKeyDefault),
		WithNoValuePolicy(options.NoValueError),
	).ApplyOptions(comp, options.JSONOptions{})
	if err == nil {
		t.Fatalf("got nil error, want <no value> residue error")
	}
	if !strings.Contains(err.Error(), options.NoValueLiteral) {
		t.Errorf("error %q does not mention the %q literal", err.Error(), options.NoValueLiteral)
	}
	if !strings.Contains(err.Error(), "net-pod") {
		t.Errorf("error %q does not contain the object name", err.Error())
	}
}

func TestNoValueResiduePolicies(t *testing.T) {
	// A top-level missing key with missingkey=zero renders "<no value>".
	applierConfigs := []struct {
		desc         string
		policy       options.NoValuePolicy
		wantErr      bool
		wantWarnings int
	}{
		{"error policy", options.NoValueError, true, 0},
		{"warn policy", options.NoValueWarn, false, 1},
		{"ignore policy", options.NoValueIgnore, false, 0},
	}
	for _, tc := range applierConfigs {
		t.Run(tc.desc, func(t *testing.T) {
			comp := mustComponent(t, missingValueComponent)
			stderr := testutil.CaptureStderr(t, func() {
				applier := NewApplier(
					WithGoTmplOptions(options.MissingKeyZero),
					WithNoValuePolicy(tc.policy),
				)
				comp, err := applier.ApplyOptions(comp, options.JSONOptions{})
				if tc.wantErr && err == nil {
					t.Errorf("got nil error, want residue error")
				}
				if !tc.wantErr && err != nil {
					t.Errorf("got error %v, want nil", err)
				}
				if tc.wantErr {
					return
				}
				// With the lenient rendering, the residue literal lands in
				// the rendered object.
				out, err := converter.FromObject(comp).ToYAMLString()
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(out, options.NoValueLiteral) {
					t.Errorf("rendered component does not contain the expected %q residue: %s", options.NoValueLiteral, out)
				}
			})
			if got := strings.Count(stderr, "rendered output contains"); got != tc.wantWarnings {
				t.Errorf("got %d warnings (%q), want %d", got, stderr, tc.wantWarnings)
			}
		})
	}
}

// scopedComponent renders .ns and .tier from options.
const scopedComponent = `kind: Component
spec:
  componentName: etcd
  version: 1.2.3
  objects:
  - kind: ObjectTemplate
    metadata:
      name: etcd-pod
    type: go-template
    template: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: etcd-pod
        namespace: '{{.ns}}'
        labels:
          tier: '{{.tier}}'`

func renderScoped(t *testing.T, compYAML string, opts options.JSONOptions) string {
	t.Helper()
	comp := mustComponent(t, compYAML)
	newComp, err := NewApplier().ApplyOptions(comp, opts)
	if err != nil {
		t.Fatalf("ApplyOptions: %v", err)
	}
	out, err := converter.FromObject(newComp).ToYAMLString()
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestComponentScopedOptions(t *testing.T) {
	testCases := []struct {
		desc         string
		component    string
		opts         options.JSONOptions
		expSubstrs   []string
		notExpSubstr []string
	}{
		{
			desc:      "component-level options override global options",
			component: scopedComponent,
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
			desc:      "name@version wins over name",
			component: scopedComponent,
			opts: options.JSONOptions{
				"ns":   "global-ns",
				"tier": "backend",
				"components": map[string]interface{}{
					"etcd":       map[string]interface{}{"ns": "etcd-ns"},
					"etcd@1.2.3": map[string]interface{}{"ns": "etcd-ns-123"},
				},
			},
			expSubstrs:   []string{"namespace: etcd-ns-123"},
			notExpSubstr: []string{"etcd-ns\n", "global-ns"},
		},
		{
			desc:      "non-matching component gets global options",
			component: scopedComponent,
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
		{
			desc:      "prefixed scoping key with business components key",
			component: scopedComponent,
			opts: options.JSONOptions{
				"ns":         "global-ns",
				"tier":       "backend",
				"components": "business-value",
				"bundle.gke.io/components": map[string]interface{}{
					"etcd": map[string]interface{}{"ns": "etcd-ns"},
				},
			},
			expSubstrs:   []string{"namespace: etcd-ns"},
			notExpSubstr: []string{"global-ns"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			out := renderScoped(t, tc.component, tc.opts)
			for _, want := range tc.expSubstrs {
				if !strings.Contains(out, want) {
					t.Errorf("rendered output\n%s\ndoes not contain %q", out, want)
				}
			}
			for _, notWant := range tc.notExpSubstr {
				if strings.Contains(out, notWant) {
					t.Errorf("rendered output\n%s\nunexpectedly contains %q", out, notWant)
				}
			}
			// The reserved scoping key must never leak into the rendered
			// component.
			if strings.Contains(out, "bundle.gke.io/components") {
				t.Errorf("rendered output\n%s\ncontains the reserved scoping key", out)
			}
		})
	}
}

// TestSafeYAMLVsStandardTemplater pins the difference between the safe YAML
// templater (annotation bundle.gke.io/safe-yaml) and the standard templater:
// a value that would inject a new YAML key is silently applied by the
// standard templater, while the safe YAML templater rejects it.
func TestSafeYAMLVsStandardTemplater(t *testing.T) {
	component := func(annotations string) string {
		return `kind: Component
spec:
  componentName: data-component
  objects:
  - kind: ObjectTemplate
    metadata:
      name: inject-pod` + annotations + `
    type: go-template
    template: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: inject-pod
      spec:
        dnsPolicy: {{.DNSPolicy}}`
	}

	injection := options.JSONOptions{"DNSPolicy": "ClusterFirst\n  injected-key: injected-value"}

	// Standard templater: the injection succeeds and the extra key appears
	// in the rendered object.
	comp := mustComponent(t, component(""))
	newComp, err := NewApplier().ApplyOptions(comp, injection)
	if err != nil {
		t.Fatalf("standard templater: unexpected error: %v", err)
	}
	out, err := converter.FromObject(newComp).ToYAMLString()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "injected-key") {
		t.Errorf("standard templater: expected injected key to pass through, got:\n%s", out)
	}

	// Safe YAML templater: the same injection is rejected.
	safeComp := mustComponent(t, component(`
      annotations:
        bundle.gke.io/safe-yaml: "true"`))
	_, err = NewApplier().ApplyOptions(safeComp, injection)
	if err == nil {
		t.Fatalf("safe YAML templater: got nil error, want injection error")
	}
	if !strings.Contains(err.Error(), "YAML Injection Detected") {
		t.Errorf("safe YAML templater: error %q is not the expected injection error", err.Error())
	}
}

// TestApplyOptionsObjectOrder pins the existing ordering: objects that do
// not match the template kind come first, rendered objects are appended
// after.
func TestApplyOptionsObjectOrder(t *testing.T) {
	comp := mustComponent(t, `kind: Component
spec:
  componentName: data-component
  objects:
  - kind: ObjectTemplate
    metadata:
      name: tmpl-pod
    type: go-template
    template: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: rendered-pod
  - kind: ConfigMap
    metadata:
      name: first-cm
  - kind: ConfigMap
    metadata:
      name: second-cm`)

	newComp, err := NewApplier().ApplyOptions(comp, options.JSONOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range newComp.Spec.Objects {
		names = append(names, o.GetName())
	}
	want := []string{"first-cm", "second-cm", "rendered-pod"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("object order = %v, want %v (unmatched objects first, rendered objects appended)", names, want)
	}
}
