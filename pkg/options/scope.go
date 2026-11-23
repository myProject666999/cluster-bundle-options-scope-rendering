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
	"fmt"
	"sort"
	"strings"

	bundle "github.com/GoogleCloudPlatform/k8s-cluster-bundle/pkg/apis/bundle/v1alpha1"
	log "k8s.io/klog"
)

const (
	// ScopedOptionsKey is a reserved top-level key in an options file. Its
	// value must be an object mapping a component name — or a
	// "name@version" string — to an object of options that only apply to
	// that component. Component-scoped options override global (top-level)
	// options, and "name@version" entries override plain "name" entries.
	//
	// Options files that do not contain this key behave exactly as before:
	// every top-level key is a global option.
	ScopedOptionsKey = "components"

	// ScopedOptionsKeyPrefixed is an unambiguous alias for ScopedOptionsKey.
	// Because it carries the bundle.gke.io prefix, it can never collide with
	// a business option key. When a file uses this key for scoping, a plain
	// top-level "components" key in the same file is treated as an ordinary
	// (business) option. This is the escape hatch for options files that
	// legitimately need a business option named "components".
	ScopedOptionsKeyPrefixed = "bundle.gke.io/components"
)

// UnknownComponentPolicy determines how the appliers react when an options
// file contains scoped options for a component that is not being rendered.
type UnknownComponentPolicy int

const (
	// UnknownComponentWarn logs a warning for every scoped component key
	// that matches no component. This is the default: a typo in a component
	// name would otherwise silently disable the scoped options.
	UnknownComponentWarn UnknownComponentPolicy = iota

	// UnknownComponentError turns unknown scoped component keys into an
	// error.
	UnknownComponentError

	// UnknownComponentIgnore silently ignores unknown scoped component keys.
	UnknownComponentIgnore
)

// NoValuePolicy determines how the template appliers react when rendered
// output still contains the "<no value>" literal that text/template emits
// for missing or nil map values (under missingkey=default/invalid/zero).
type NoValuePolicy int

const (
	// NoValueWarn logs a warning when rendered output contains the
	// "<no value>" literal. This is the default.
	NoValueWarn NoValuePolicy = iota

	// NoValueError returns an error when rendered output contains the
	// "<no value>" literal.
	NoValueError

	// NoValueIgnore disables the "<no value>" residue check.
	NoValueIgnore
)

// NoValueLiteral is the string that text/template prints for an invalid
// (missing) map entry, and also for a nil interface{} value produced by
// missingkey=zero. Note that under missingkey=zero, accessing a missing
// nested key of a map[string]interface{} yields a nil interface{}, which
// also renders as this literal — so the residue check is the only reliable
// way to catch such cases.
const NoValueLiteral = "<no value>"

// CheckNoValueResidue inspects rendered template output for the NoValueLiteral
// and acts according to the given policy. The context string should identify
// the template being rendered (component reference, object name); it must not
// contain option values, only key paths and identifiers.
func CheckNoValueResidue(rendered, context string, policy NoValuePolicy) error {
	if policy == NoValueIgnore || !strings.Contains(rendered, NoValueLiteral) {
		return nil
	}
	msg := fmt.Sprintf("%s: rendered output contains the %q literal, which indicates a missing or nil option value; "+
		"if this literal is intentional, disable the check with a NoValueIgnore policy", context, NoValueLiteral)
	if policy == NoValueError {
		return fmt.Errorf("%s", msg)
	}
	log.Warningf("%s", msg)
	return nil
}

// MergeShallowOptions merges overlay onto base using a top-level shallow
// merge: every top-level key of overlay replaces the entire value (scalar,
// map, or slice) of the same key in base. Nested maps are NOT merged
// recursively — for example, merging {"net": {"cidr": "..."}} with
// {"net": {"mtu": 1500}} yields {"net": {"mtu": 1500}}; the cidr entry is
// lost. A null (nil) value in overlay is stored as-is; it is not treated as
// a deletion. Keys are case-sensitive and dots in keys are not expanded into
// nested paths. Neither input is modified.
func MergeShallowOptions(base, overlay JSONOptions) JSONOptions {
	out := make(JSONOptions, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		out[k] = v
	}
	return out
}

// SplitScopedOptions splits opts into global options and component-scoped
// options. The returned global options never contain the reserved scoping
// key. If neither reserved key is present, the returned scoped map is nil
// and the global options are a shallow copy of opts.
//
// If the prefixed key (ScopedOptionsKeyPrefixed) is present, it is used as
// the scoping block and a plain "components" key, if any, remains a normal
// business option. Otherwise a plain ScopedOptionsKey key is used.
//
// Errors reference key paths only, never option values.
func SplitScopedOptions(opts JSONOptions) (global JSONOptions, scoped map[string]JSONOptions, err error) {
	if opts == nil {
		return nil, nil, nil
	}

	keyUsed := ""
	scopedSrc, ok := opts[ScopedOptionsKeyPrefixed]
	if ok {
		keyUsed = ScopedOptionsKeyPrefixed
	} else if scopedSrc, ok = opts[ScopedOptionsKey]; ok {
		keyUsed = ScopedOptionsKey
	}

	global = make(JSONOptions, len(opts))
	for k, v := range opts {
		if k == keyUsed {
			continue
		}
		global[k] = v
	}

	if keyUsed == "" {
		return global, nil, nil
	}

	scopedMap, ok := scopedSrc.(map[string]interface{})
	if !ok {
		return nil, nil, fmt.Errorf("reserved options key %q must contain an object mapping component names to option objects", keyUsed)
	}
	scoped = make(map[string]JSONOptions, len(scopedMap))
	for compKey, compVal := range scopedMap {
		compMap, ok := compVal.(map[string]interface{})
		if !ok {
			return nil, nil, fmt.Errorf("scoped options at key path %q.%q must be an object of option key-value pairs", keyUsed, compKey)
		}
		scoped[compKey] = JSONOptions(compMap)
	}
	return global, scoped, nil
}

// MergeScopedOptions merges two scoped-options maps (as produced by
// SplitScopedOptions). Entries for the same component key are combined with
// a top-level shallow merge, with overlay winning. Component keys present in
// only one of the maps are preserved as-is: a file that does not mention a
// component never deletes another file's configuration for that component.
// Neither input is modified.
func MergeScopedOptions(base, overlay map[string]JSONOptions) map[string]JSONOptions {
	if base == nil && overlay == nil {
		return nil
	}
	out := make(map[string]JSONOptions, len(base)+len(overlay))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range overlay {
		if existing, ok := out[k]; ok {
			out[k] = MergeShallowOptions(existing, v)
		} else {
			out[k] = v
		}
	}
	return out
}

// CombineScopedOptions recombines global and scoped options into a single
// JSONOptions value, reversing SplitScopedOptions. The scoped block is
// stored under ScopedOptionsKey, unless the global options already contain a
// business key named "components", in which case ScopedOptionsKeyPrefixed is
// used to avoid clobbering it. If scoped is empty, the global options are
// returned unchanged.
func CombineScopedOptions(global JSONOptions, scoped map[string]JSONOptions) JSONOptions {
	if len(scoped) == 0 {
		return global
	}
	out := make(JSONOptions, len(global)+1)
	for k, v := range global {
		out[k] = v
	}
	key := ScopedOptionsKey
	if _, conflict := global[ScopedOptionsKey]; conflict {
		key = ScopedOptionsKeyPrefixed
	}
	scopedMap := make(map[string]interface{}, len(scoped))
	for k, v := range scoped {
		scopedMap[k] = map[string]interface{}(v)
	}
	out[key] = scopedMap
	return out
}

// scopedKeyForRef returns the "name@version" scoped key for a component
// reference, or "" if the reference has no version.
func scopedKeyForRef(ref bundle.ComponentReference) string {
	if ref.Version == "" {
		return ""
	}
	return ref.ComponentName + "@" + ref.Version
}

// OptionsForComponent resolves the effective options for a single component:
// the global options, overlaid with the scoped block matching the component
// name, overlaid again with the scoped block matching "name@version". Each
// overlay step is a top-level shallow merge (see MergeShallowOptions), so
// the more specific "name@version" entries win over "name" entries, which in
// turn win over global entries. The reserved scoping key is never visible in
// the result.
func OptionsForComponent(ref bundle.ComponentReference, opts JSONOptions) (JSONOptions, error) {
	global, scoped, err := SplitScopedOptions(opts)
	if err != nil {
		return nil, err
	}
	if len(scoped) == 0 {
		return global, nil
	}
	out := global
	if block, ok := scoped[ref.ComponentName]; ok {
		out = MergeShallowOptions(out, block)
	}
	if key := scopedKeyForRef(ref); key != "" {
		if block, ok := scoped[key]; ok {
			out = MergeShallowOptions(out, block)
		}
	}
	return out, nil
}

// ScopedOptionKeys returns the sorted list of component keys found in the
// scoped options block of opts, or nil if opts has no scoping block.
func ScopedOptionKeys(opts JSONOptions) ([]string, error) {
	_, scoped, err := SplitScopedOptions(opts)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(scoped))
	for k := range scoped {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys, nil
}

// UnmatchedScopedKeys returns the sorted list of scoped component keys that
// match neither the component name nor its "name@version" form. Callers can
// use this to warn about (or reject) scoped options that reference
// components not being rendered — a common symptom of a typo that would
// otherwise silently disable the scoped options.
func UnmatchedScopedKeys(opts JSONOptions, ref bundle.ComponentReference) ([]string, error) {
	keys, err := ScopedOptionKeys(opts)
	if err != nil {
		return nil, err
	}
	versioned := scopedKeyForRef(ref)
	var unmatched []string
	for _, k := range keys {
		if k == ref.ComponentName || (versioned != "" && k == versioned) {
			continue
		}
		unmatched = append(unmatched, k)
	}
	return unmatched, nil
}
