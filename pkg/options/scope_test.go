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

package options

import (
	"reflect"
	"strings"
	"testing"

	bundle "github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/apis/bundle/v1alpha1"
)

func TestMergeShallowOptions(t *testing.T) {
	testCases := []struct {
		desc     string
		base     JSONOptions
		overlay  JSONOptions
		expected JSONOptions
	}{
		{
			desc:     "disjoint keys",
			base:     JSONOptions{"a": 1},
			overlay:  JSONOptions{"b": 2},
			expected: JSONOptions{"a": 1, "b": 2},
		},
		{
			desc:     "scalar replaced",
			base:     JSONOptions{"a": 1},
			overlay:  JSONOptions{"a": 2},
			expected: JSONOptions{"a": 2},
		},
		{
			// Documented behavior: nested maps are replaced wholesale, not
			// merged. {"net": {"cidr": ...}} + {"net": {"mtu": 1500}} loses
			// cidr.
			desc:     "nested map replaced wholesale",
			base:     JSONOptions{"net": map[string]interface{}{"cidr": "10.0.0.0/8"}},
			overlay:  JSONOptions{"net": map[string]interface{}{"mtu": 1500}},
			expected: JSONOptions{"net": map[string]interface{}{"mtu": 1500}},
		},
		{
			desc:     "array replaced wholesale",
			base:     JSONOptions{"addrs": []interface{}{"a", "b"}},
			overlay:  JSONOptions{"addrs": []interface{}{"c"}},
			expected: JSONOptions{"addrs": []interface{}{"c"}},
		},
		{
			// null is a value, not a deletion.
			desc:     "null is stored, not a delete",
			base:     JSONOptions{"a": map[string]interface{}{"x": 1}},
			overlay:  JSONOptions{"a": nil},
			expected: JSONOptions{"a": nil},
		},
		{
			desc:     "keys are case sensitive",
			base:     JSONOptions{"Foo": 1},
			overlay:  JSONOptions{"foo": 2},
			expected: JSONOptions{"Foo": 1, "foo": 2},
		},
		{
			desc:     "dots in keys are not expanded",
			base:     JSONOptions{"a": map[string]interface{}{"b": 1}},
			overlay:  JSONOptions{"a.b": 2},
			expected: JSONOptions{"a": map[string]interface{}{"b": 1}, "a.b": 2},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got := MergeShallowOptions(tc.base, tc.overlay)
			if !reflect.DeepEqual(got, tc.expected) {
				t.Errorf("MergeShallowOptions(%v, %v) = %v, want %v", tc.base, tc.overlay, got, tc.expected)
			}
		})
	}
}

func TestSplitScopedOptions(t *testing.T) {
	testCases := []struct {
		desc          string
		opts          JSONOptions
		wantGlobal    JSONOptions
		wantScoped    map[string]JSONOptions
		wantErrSubstr string
		// If set, the error must NOT contain this string (option values must
		// not leak into error messages).
		errNotSubstr string
	}{
		{
			desc:       "nil options",
			opts:       nil,
			wantGlobal: nil,
			wantScoped: nil,
		},
		{
			desc:       "no scoping key",
			opts:       JSONOptions{"a": 1, "b": "two"},
			wantGlobal: JSONOptions{"a": 1, "b": "two"},
			wantScoped: nil,
		},
		{
			desc: "plain scoping key",
			opts: JSONOptions{
				"a": 1,
				"components": map[string]interface{}{
					"etcd": map[string]interface{}{"ns": "etcd-ns"},
				},
			},
			wantGlobal: JSONOptions{"a": 1},
			wantScoped: map[string]JSONOptions{
				"etcd": {"ns": "etcd-ns"},
			},
		},
		{
			desc: "prefixed scoping key",
			opts: JSONOptions{
				"a": 1,
				"bundle.gke.io/components": map[string]interface{}{
					"etcd": map[string]interface{}{"ns": "etcd-ns"},
				},
			},
			wantGlobal: JSONOptions{"a": 1},
			wantScoped: map[string]JSONOptions{
				"etcd": {"ns": "etcd-ns"},
			},
		},
		{
			desc: "prefixed key escapes a business 'components' key",
			opts: JSONOptions{
				"components": "business-value",
				"bundle.gke.io/components": map[string]interface{}{
					"etcd": map[string]interface{}{"ns": "etcd-ns"},
				},
			},
			wantGlobal: JSONOptions{"components": "business-value"},
			wantScoped: map[string]JSONOptions{
				"etcd": {"ns": "etcd-ns"},
			},
		},
		{
			desc: "scoping key must be an object",
			opts: JSONOptions{
				"components": "secret-business-value",
			},
			wantErrSubstr: `"components"`,
			errNotSubstr:  "secret-business-value",
		},
		{
			desc: "scoped component entry must be an object",
			opts: JSONOptions{
				"components": map[string]interface{}{
					"etcd": "secret-component-value",
				},
			},
			wantErrSubstr: `"components"."etcd"`,
			errNotSubstr:  "secret-component-value",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			global, scoped, err := SplitScopedOptions(tc.opts)
			if tc.wantErrSubstr != "" {
				if err == nil {
					t.Fatalf("SplitScopedOptions(%v) succeeded, want error containing %q", tc.opts, tc.wantErrSubstr)
				}
				if !strings.Contains(err.Error(), tc.wantErrSubstr) {
					t.Errorf("error %q does not contain key path %q", err.Error(), tc.wantErrSubstr)
				}
				if tc.errNotSubstr != "" && strings.Contains(err.Error(), tc.errNotSubstr) {
					t.Errorf("error %q leaks option value %q", err.Error(), tc.errNotSubstr)
				}
				return
			}
			if err != nil {
				t.Fatalf("SplitScopedOptions(%v) returned error: %v", tc.opts, err)
			}
			if !reflect.DeepEqual(global, tc.wantGlobal) {
				t.Errorf("global = %v, want %v", global, tc.wantGlobal)
			}
			if !reflect.DeepEqual(scoped, tc.wantScoped) {
				t.Errorf("scoped = %v, want %v", scoped, tc.wantScoped)
			}
		})
	}
}

func TestMergeScopedOptions(t *testing.T) {
	base := map[string]JSONOptions{
		"etcd":   {"ns": "etcd-ns", "tier": "backend"},
		"worker": {"replicas": 3},
	}
	overlay := map[string]JSONOptions{
		// Shallow-merges into the existing etcd block.
		"etcd": {"ns": "etcd-other"},
		// A brand-new component.
		"scheduler": {"policy": "spread"},
	}
	got := MergeScopedOptions(base, overlay)
	want := map[string]JSONOptions{
		"etcd":      {"ns": "etcd-other", "tier": "backend"},
		"worker":    {"replicas": 3},
		"scheduler": {"policy": "spread"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MergeScopedOptions() = %v, want %v", got, want)
	}
	// The overlay not mentioning "worker" must not have deleted it, and the
	// inputs must be unmodified.
	if _, ok := base["scheduler"]; ok {
		t.Errorf("base was modified: %v", base)
	}
}

func TestCombineScopedOptionsRoundTrip(t *testing.T) {
	global := JSONOptions{"a": 1}
	scoped := map[string]JSONOptions{"etcd": {"ns": "etcd-ns"}}

	combined := CombineScopedOptions(global, scoped)
	if _, ok := combined[ScopedOptionsKey]; !ok {
		t.Fatalf("combined options %v do not contain key %q", combined, ScopedOptionsKey)
	}
	gotGlobal, gotScoped, err := SplitScopedOptions(combined)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotGlobal, global) {
		t.Errorf("global = %v, want %v", gotGlobal, global)
	}
	if !reflect.DeepEqual(gotScoped, scoped) {
		t.Errorf("scoped = %v, want %v", gotScoped, scoped)
	}
}

func TestCombineScopedOptionsBusinessKeyConflict(t *testing.T) {
	// A business option literally named "components" must survive
	// recombination: the scoped block moves to the prefixed key.
	global := JSONOptions{"components": "business-value"}
	scoped := map[string]JSONOptions{"etcd": {"ns": "etcd-ns"}}

	combined := CombineScopedOptions(global, scoped)
	if combined["components"] != "business-value" {
		t.Errorf("business 'components' key was clobbered: %v", combined)
	}
	if _, ok := combined[ScopedOptionsKeyPrefixed]; !ok {
		t.Fatalf("combined options %v do not contain prefixed key %q", combined, ScopedOptionsKeyPrefixed)
	}

	gotGlobal, gotScoped, err := SplitScopedOptions(combined)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotGlobal, global) {
		t.Errorf("global = %v, want %v", gotGlobal, global)
	}
	if !reflect.DeepEqual(gotScoped, scoped) {
		t.Errorf("scoped = %v, want %v", gotScoped, scoped)
	}
}

func TestOptionsForComponent(t *testing.T) {
	opts := JSONOptions{
		"ns":   "global-ns",
		"tier": "backend",
		"components": map[string]interface{}{
			"etcd": map[string]interface{}{
				"ns": "etcd-ns",
			},
			"etcd@1.2.3": map[string]interface{}{
				"ns":   "etcd-ns-123",
				"size": "large",
			},
			"other": map[string]interface{}{
				"ns": "other-ns",
			},
		},
	}

	testCases := []struct {
		desc string
		ref  bundle.ComponentReference
		want JSONOptions
	}{
		{
			desc: "name match overrides global",
			ref:  bundle.ComponentReference{ComponentName: "etcd", Version: "9.9.9"},
			want: JSONOptions{"ns": "etcd-ns", "tier": "backend"},
		},
		{
			desc: "name@version wins over name",
			ref:  bundle.ComponentReference{ComponentName: "etcd", Version: "1.2.3"},
			want: JSONOptions{"ns": "etcd-ns-123", "tier": "backend", "size": "large"},
		},
		{
			desc: "unmatched component gets global options",
			ref:  bundle.ComponentReference{ComponentName: "worker", Version: "1.0.0"},
			want: JSONOptions{"ns": "global-ns", "tier": "backend"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got, err := OptionsForComponent(tc.ref, opts)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("OptionsForComponent(%v) = %v, want %v", tc.ref, got, tc.want)
			}
		})
	}
}

func TestOptionsForComponentShallowSemantics(t *testing.T) {
	// Component-level overrides are also top-level shallow: a nested map in
	// the component block replaces the whole nested map from the global
	// block.
	opts := JSONOptions{
		"net": map[string]interface{}{"cidr": "10.0.0.0/8"},
		"components": map[string]interface{}{
			"etcd": map[string]interface{}{
				"net": map[string]interface{}{"mtu": 1500},
			},
		},
	}
	got, err := OptionsForComponent(bundle.ComponentReference{ComponentName: "etcd", Version: "1.0.0"}, opts)
	if err != nil {
		t.Fatal(err)
	}
	want := JSONOptions{"net": map[string]interface{}{"mtu": 1500}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("OptionsForComponent() = %v, want %v (nested maps are replaced, not merged)", got, want)
	}
}

func TestUnmatchedScopedKeys(t *testing.T) {
	opts := JSONOptions{
		"components": map[string]interface{}{
			"etcd":       map[string]interface{}{"ns": "a"},
			"etcd@1.0.0": map[string]interface{}{"ns": "b"},
			"ghost":      map[string]interface{}{"ns": "c"},
		},
	}

	got, err := UnmatchedScopedKeys(opts, bundle.ComponentReference{ComponentName: "etcd", Version: "1.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"ghost"}) {
		t.Errorf("UnmatchedScopedKeys() = %v, want [ghost]", got)
	}

	got, err = UnmatchedScopedKeys(opts, bundle.ComponentReference{ComponentName: "worker", Version: "2.0.0"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, []string{"etcd", "etcd@1.0.0", "ghost"}) {
		t.Errorf("UnmatchedScopedKeys() = %v, want [etcd etcd@1.0.0 ghost]", got)
	}
}

func TestCheckNoValueResidue(t *testing.T) {
	rendered := "image: '<no value>'"

	if err := CheckNoValueResidue(rendered, "object template \"pod\"", NoValueIgnore); err != nil {
		t.Errorf("NoValueIgnore: got error %v, want nil", err)
	}
	if err := CheckNoValueResidue(rendered, "object template \"pod\"", NoValueWarn); err != nil {
		t.Errorf("NoValueWarn: got error %v, want nil", err)
	}
	if err := CheckNoValueResidue(rendered, "object template \"pod\"", NoValueError); err == nil {
		t.Errorf("NoValueError: got nil error, want non-nil")
	} else {
		if !strings.Contains(err.Error(), "<no value>") {
			t.Errorf("error %q does not mention the residue literal", err.Error())
		}
		if !strings.Contains(err.Error(), "object template") {
			t.Errorf("error %q does not contain the context", err.Error())
		}
	}

	clean := "image: 'my-image'"
	if err := CheckNoValueResidue(clean, "ctx", NoValueError); err != nil {
		t.Errorf("clean output: got error %v, want nil", err)
	}
}
