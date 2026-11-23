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

package cmdlib

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/options"
	"github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/testutil"
)

// merge merges the given (path, contents) files in order and returns the
// result, failing the test on error.
func merge(t *testing.T, files ...testutil.FilePair) options.JSONOptions {
	t.Helper()
	rw := testutil.NewEmptyReaderWriter()
	var paths []string
	for _, f := range files {
		rw.ReadFiles[f.Path] = f.Contents
		paths = append(paths, f.Path)
	}
	opts, err := MergeOptions(context.Background(), rw, paths)
	if err != nil {
		t.Fatalf("MergeOptions(%v): %v", paths, err)
	}
	return opts
}

func TestMergeOptionsShallowSemantics(t *testing.T) {
	testCases := []struct {
		desc  string
		files []testutil.FilePair
		want  options.JSONOptions
	}{
		{
			desc: "nested map replaced wholesale (net.cidr lost to net.mtu)",
			files: []testutil.FilePair{
				{Path: "/a.yaml", Contents: "net:\n  cidr: 10.0.0.0/8"},
				{Path: "/b.yaml", Contents: "net:\n  mtu: 1500"},
			},
			want: options.JSONOptions{
				"net": map[string]interface{}{"mtu": float64(1500)},
			},
		},
		{
			desc: "array replaced wholesale",
			files: []testutil.FilePair{
				{Path: "/a.yaml", Contents: "addrs: [a, b]"},
				{Path: "/b.yaml", Contents: "addrs: [c]"},
			},
			want: options.JSONOptions{
				"addrs": []interface{}{"c"},
			},
		},
		{
			desc: "null is stored, not a deletion",
			files: []testutil.FilePair{
				{Path: "/a.yaml", Contents: "a:\n  x: 1"},
				{Path: "/b.yaml", Contents: "a: null"},
			},
			want: options.JSONOptions{"a": nil},
		},
		{
			desc: "scalar replaced",
			files: []testutil.FilePair{
				{Path: "/a.yaml", Contents: "replicas: 1"},
				{Path: "/b.yaml", Contents: "replicas: 3"},
			},
			want: options.JSONOptions{"replicas": float64(3)},
		},
		{
			desc: "keys are case sensitive",
			files: []testutil.FilePair{
				{Path: "/a.yaml", Contents: "Foo: one"},
				{Path: "/b.yaml", Contents: "foo: two"},
			},
			want: options.JSONOptions{"Foo": "one", "foo": "two"},
		},
		{
			desc: "dots in keys are not expanded",
			files: []testutil.FilePair{
				{Path: "/a.yaml", Contents: "a:\n  b: nested"},
				{Path: "/b.yaml", Contents: "a.b: flat"},
			},
			want: options.JSONOptions{
				"a":   map[string]interface{}{"b": "nested"},
				"a.b": "flat",
			},
		},
		{
			desc: "json files merge with yaml files",
			files: []testutil.FilePair{
				{Path: "/a.yaml", Contents: "a: 1"},
				{Path: "/b.json", Contents: `{"b": 2}`},
			},
			want: options.JSONOptions{"a": float64(1), "b": float64(2)},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			got := merge(t, tc.files...)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("MergeOptions() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestMergeOptionsScopedAcrossFiles(t *testing.T) {
	// Scoped data from each file is split out before merging: a file that
	// does not mention a component must not delete another file's scoped
	// configuration for that component, and blocks for the same component
	// shallow-merge.
	got := merge(t,
		testutil.FilePair{Path: "/a.yaml", Contents: `
ns: global-ns
components:
  etcd:
    ns: etcd-ns
    tier: backend
  worker:
    replicas: 3
`},
		testutil.FilePair{Path: "/b.yaml", Contents: `
components:
  etcd:
    ns: etcd-ns-override
  scheduler:
    policy: spread
`},
	)

	want := options.JSONOptions{
		"ns": "global-ns",
		"components": map[string]interface{}{
			"etcd":      map[string]interface{}{"ns": "etcd-ns-override", "tier": "backend"},
			"worker":    map[string]interface{}{"replicas": float64(3)},
			"scheduler": map[string]interface{}{"policy": "spread"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("MergeOptions() = %v, want %v", got, want)
	}
}

func TestMergeOptionsBusinessComponentsKeyEscape(t *testing.T) {
	// A business option literally named "components" survives when scoping uses
	// the prefixed reserved key.
	got := merge(t,
		testutil.FilePair{Path: "/a.yaml", Contents: `
components: business-value
bundle.gke.io/components:
  etcd:
    ns: etcd-ns
`},
	)

	if got["components"] != "business-value" {
		t.Errorf("business 'components' key was clobbered: %v", got)
	}
	scoped, ok := got["bundle.gke.io/components"].(map[string]interface{})
	if !ok {
		t.Fatalf("prefixed scoped block missing from %v", got)
	}
	if _, ok := scoped["etcd"]; !ok {
		t.Errorf("scoped etcd block missing from %v", scoped)
	}
}

func TestMergeOptionsFileErrors(t *testing.T) {
	testCases := []struct {
		desc         string
		files        []testutil.FilePair
		paths        []string
		wantErrParts []string
	}{
		{
			desc: "empty file",
			files: []testutil.FilePair{
				{Path: "/empty.yaml", Contents: ""},
			},
			wantErrParts: []string{"/empty.yaml", "empty"},
		},
		{
			desc: "comment-only file is empty",
			files: []testutil.FilePair{
				{Path: "/comments.yaml", Contents: "# nothing here\n"},
			},
			wantErrParts: []string{"/comments.yaml", "empty"},
		},
		{
			desc: "unrecognized extension",
			files: []testutil.FilePair{
				{Path: "/opts.txt", Contents: "a: 1"},
			},
			wantErrParts: []string{"/opts.txt", "extension"},
		},
		{
			desc: "top-level array",
			files: []testutil.FilePair{
				{Path: "/arr.yaml", Contents: "- one\n- two\n"},
			},
			wantErrParts: []string{"/arr.yaml", "object", "array"},
		},
		{
			desc: "top-level scalar",
			files: []testutil.FilePair{
				{Path: "/scalar.yaml", Contents: "just-a-string"},
			},
			wantErrParts: []string{"/scalar.yaml", "object", "scalar"},
		},
		{
			desc: "malformed scoped block",
			files: []testutil.FilePair{
				{Path: "/scoped.yaml", Contents: "components: not-an-object"},
			},
			wantErrParts: []string{"/scoped.yaml", "components"},
		},
		{
			desc:         "unreadable file",
			paths:        []string{"/does-not-exist.yaml"},
			wantErrParts: []string{"/does-not-exist.yaml", "unreadable"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.desc, func(t *testing.T) {
			rw := testutil.NewEmptyReaderWriter()
			var paths []string
			for _, f := range tc.files {
				rw.ReadFiles[f.Path] = f.Contents
				paths = append(paths, f.Path)
			}
			paths = append(paths, tc.paths...)

			_, err := MergeOptions(context.Background(), rw, paths)
			if err == nil {
				t.Fatalf("got nil error, want error containing %v", tc.wantErrParts)
			}
			for _, want := range tc.wantErrParts {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err.Error(), want)
				}
			}
		})
	}
}

func TestMergeOptionsDuplicateFileWarns(t *testing.T) {
	rw := testutil.NewEmptyReaderWriter()
	rw.ReadFiles["/a.yaml"] = "a: 1"

	var opts options.JSONOptions
	var err error
	stderr := testutil.CaptureStderr(t, func() {
		opts, err = MergeOptions(context.Background(), rw, []string{"/a.yaml", "/a.yaml"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(stderr, "multiple times"); got != 1 {
		t.Errorf("got %d duplicate-file warnings (%q), want 1", got, stderr)
	}
	// The file is still merged (idempotently) at each position.
	if !reflect.DeepEqual(opts, options.JSONOptions{"a": float64(1)}) {
		t.Errorf("MergeOptions() = %v, want {a: 1}", opts)
	}
}
