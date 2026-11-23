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

package multi

import (
	"strings"
	"testing"

	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/converter"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/options"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/testutil"
)

const scopedMultiComponent = `
kind: Component
spec:
  componentName: data-component
  version: 1.0.0
  objects:
  - kind: ObjectTemplate
    type: go-template
    template: |
      apiVersion: v1
      kind: Pod
      metadata:
        name: logger-pod
        namespace: {{.ns}}
`

func TestMultiScopedOptionsEndToEnd(t *testing.T) {
	comp, err := converter.FromYAMLString(scopedMultiComponent).ToComponent()
	if err != nil {
		t.Fatal(err)
	}

	opts := options.JSONOptions{
		"ns": "global-ns",
		"components": map[string]interface{}{
			"data-component": map[string]interface{}{"ns": "scoped-ns"},
		},
	}

	newComp, err := NewDefaultApplier().ApplyOptions(comp, opts)
	if err != nil {
		t.Fatal(err)
	}
	out, err := converter.FromObject(newComp).ToYAMLString()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "namespace: scoped-ns") {
		t.Errorf("expected component-scoped option to win, got:\n%s", out)
	}
	if strings.Contains(out, "global-ns") {
		t.Errorf("global option should have been overridden, got:\n%s", out)
	}
}

func TestMultiUnknownComponentPolicies(t *testing.T) {
	makeOpts := func() options.JSONOptions {
		return options.JSONOptions{
			"ns": "global-ns",
			"components": map[string]interface{}{
				"data-component": map[string]interface{}{"ns": "scoped-ns"},
				"ghost-a":        map[string]interface{}{"ns": "a"},
				"ghost-b@2.0.0":  map[string]interface{}{"ns": "b"},
			},
		}
	}

	testCases := []struct {
		desc         string
		applier      options.Applier
		wantErr      bool
		wantWarnings int
	}{
		{
			desc:         "default policy warns once per unknown component",
			applier:      NewDefaultApplier(),
			wantWarnings: 2,
		},
		{
			desc:         "warn policy explicitly",
			applier:      NewDefaultApplierWithConfig(WithUnknownComponentPolicy(options.UnknownComponentWarn)),
			wantWarnings: 2,
		},
		{
			desc:    "error policy",
			applier: NewDefaultApplierWithConfig(WithUnknownComponentPolicy(options.UnknownComponentError)),
			wantErr: true,
		},
		{
			desc:         "ignore policy",
			applier:      NewDefaultApplierWithConfig(WithUnknownComponentPolicy(options.UnknownComponentIgnore)),
			wantWarnings: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			comp, err := converter.FromYAMLString(scopedMultiComponent).ToComponent()
			if err != nil {
				t.Fatal(err)
			}
			var applyErr error
			stderr := testutil.CaptureStderr(t, func() {
				_, applyErr = tc.applier.ApplyOptions(comp, makeOpts())
			})
			if tc.wantErr {
				if applyErr == nil {
					t.Fatalf("got nil error, want unknown-component error")
				}
				if !strings.Contains(applyErr.Error(), "ghost-a") {
					t.Errorf("error %q does not name the unknown component key", applyErr.Error())
				}
				return
			}
			if applyErr != nil {
				t.Fatalf("got error %v, want nil", applyErr)
			}
			if got := strings.Count(stderr, "does not match component"); got != tc.wantWarnings {
				t.Errorf("got %d unknown-component warnings (%q), want %d", got, stderr, tc.wantWarnings)
			}
		})
	}
}
