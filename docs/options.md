# Options: missing keys, merging, and component scoping

This document describes how options (`options.JSONOptions`) are read from
files, merged, scoped to components, and applied to `ObjectTemplate`
(go-template) and `PatchTemplate` objects before a component is exported.

## Missing keys

Both template appliers now default to `missingkey=error`: referencing an
option key that was not provided is an **error**, not a silent `<no value>`.
The error identifies the component reference, the object (or patch template)
name, the position in the template, and the missing key path, e.g.:

```
for component {data-component 1.0.0}, error in applying: error executing
template for object net-pod: template: ...:8:14: executing "..." at
<.net.cidr>: map has no entry for key "net"
```

### Opting out

The historical lenient behavior of the go-template applier is available via
an explicit switch:

```go
gotmpl.NewApplier(gotmpl.WithGoTmplOptions(options.MissingKeyDefault))
gotmpl.NewApplier(gotmpl.WithGoTmplOptions(options.MissingKeyZero))
```

The constants `options.MissingKeyError`, `MissingKeyDefault`,
`MissingKeyInvalid`, and `MissingKeyZero` map to the usual
`text/template` options. `patchtmpl.WithGoTmplOptions` works the same way.

Beware that `missingkey=zero` and `missingkey=default` behave differently
for **nested** keys of a `map[string]interface{}`:

- `{{.net.cidr}}` with no `net` key, `missingkey=zero`: `net` evaluates to a
  nil `interface{}`, and accessing `.cidr` on it is itself an execution
  error (`nil pointer evaluating interface {}.cidr`).
- The same template with `missingkey=default` renders the literal
  `<no value>`.

### The `<no value>` residue check

Because lenient modes (and nil option values) can leave the literal
`<no value>` in rendered YAML — where it may parse as a string or break YAML
syntax depending on the template — both appliers check the rendered output
for that literal. The policy is configurable per applier:

```go
gotmpl.NewApplier(gotmpl.WithNoValuePolicy(options.NoValueError)) // or NoValueWarn, NoValueIgnore
patchtmpl.NewApplierWithConfig(patchtmpl.WithNoValuePolicy(options.NoValueWarn))
```

The default is `NoValueWarn`. **Limitation:** the check is a plain substring
check on the rendered output. There is no escaping mechanism; if a template
legitimately needs to emit the literal `<no value>`, disable the check with
`options.NoValueIgnore`.

## Merging options files

`cmdlib.MergeOptions` reads options files strictly in command-line order and
merges them with a **top-level shallow merge**: for every top-level key, the
later file's value — scalar, map, or array — replaces the earlier value
wholesale. Nested maps are *not* merged recursively:

```yaml
# a.yaml                    # b.yaml                 # merged result
net:                        net:                     net:
  cidr: 10.0.0.0/8            mtu: 1500                  mtu: 1500
```

The `cidr` entry is lost. This is the long-standing behavior and is
intentionally unchanged; there is no recursive merge and `null` is **not**
interpreted as a deletion — a `null` value is stored as-is and replaces
whatever was there before.

Key rules:

- Keys are **case-sensitive** (`Foo` and `foo` are different options).
- Dots in keys are **not** expanded: a key named `a.b` is one flat key, not
  the nested path `a.b`. Consequently a template reading `{{.a.b}}` cannot
  be satisfied by a key literally named `a.b`.

File-reading rules (every error names the file and the failure category):

- Unknown extension (not `.yaml`/`.yml`/`.json`): error, category
  "unrecognized extension".
- Empty (or comment-only) file: error, category "empty file".
- Top-level value is an array or scalar instead of an object: error,
  category "top-level value must be an object".
- The same file listed twice on the command line: a **warning** is logged;
  the file is still merged at each position it appears.

## Component-scoped options

An options file may contain the reserved top-level key `components`
(`options.ScopedOptionsKey`), mapping a component name — or `name@version` —
to options that apply only to that component:

```yaml
# global options: apply to every component
namespace: default
components:
  etcd:                  # only for the component named "etcd"
    namespace: etcd-ns
  etcd@1.2.3:            # only for etcd version 1.2.3
    namespace: etcd-ns-123
```

Resolution per component (`options.OptionsForComponent`): start from the
global options, overlay the block matching the component name, then overlay
the block matching `name@version`. Each overlay is the same top-level
shallow merge as file merging, so **the more specific `name@version` entry
wins** over the plain name entry, which wins over the global value. Files
without the reserved key behave exactly as before.

Merging scoped data across files: each file's scoped block is split out
*before* merging and recombined afterwards. Blocks for the same component
shallow-merge across files, and a file that does not mention a component
never deletes another file's configuration for that component.

Unknown components: scoped keys that match no component being rendered are
reported by the multi applier according to
`multi.WithUnknownComponentPolicy`: `options.UnknownComponentWarn`
(default — a typo would otherwise silently disable the options),
`UnknownComponentError`, or `UnknownComponentIgnore`. Note the applier
renders one component per `ApplyOptions` call; when looping over components,
resolve scoping up front with `options.OptionsForComponent` and check the
whole set once with `options.UnmatchedScopedKeys`.

Business-key conflict: if you need a literal business option named
`components`, use the prefixed reserved key `bundle.gke.io/components`
(`options.ScopedOptionsKeyPrefixed`) for scoping. When the prefixed key is
present, a plain `components` key is treated as an ordinary option. When
`cmdlib.MergeOptions` recombines global and scoped data and the global
options already contain a business `components` key, the scoped block is
stored under the prefixed key to avoid clobbering it.

## Schema defaults and validation

`openapi.ApplyDefaults` runs **after** option merging and scoping, and
**before** rendering, whenever a template carries an `optionsSchema`. It
fills in schema defaults without overriding explicitly provided values, and
it validates the options against the schema — so templates *with* a schema
are always validated before rendering, and templates *without* one are not
validated at all. Validation failures keep the validator's field paths
(e.g. `namespace in body is required`) and are wrapped with the component
reference and object name.

## Safe YAML templater

Objects annotated with `bundle.gke.io/safe-yaml` (or appliers constructed
with `gotmpl.WithSafeYAMLTemplaterOverride`) are rendered with the safetext
YAML templater instead of plain `text/template`. The difference: the
standard templater interpolates values verbatim, so a value containing YAML
syntax (newlines, `key:` pairs) silently changes the structure of the
rendered document — YAML injection. The safe YAML templater detects when an
interpolated value would alter the document structure and fails with
`YAML Injection Detected`. Both behaviors are pinned by tests in
`pkg/options/gotmpl`.

## Object ordering

`ApplyOptions` preserves the existing ordering: objects that do not match
the applier's template kind come first in the resulting component, and
rendered objects are appended after them. This is pinned by
`TestApplyOptionsObjectOrder`.

## Error messages and secrets

Options frequently carry certificate paths, addresses, or even passwords.
Diagnostics added in this change emit **key paths only, never option
values**. Note that template execution errors still wrap the underlying
`text/template` error, which may include values referenced by the template;
avoid logging raw template errors in contexts where that matters.

Rendered objects remain available for object validation by callers (for
example `pkg/validate`); validation happens after `ApplyOptions` returns,
and failures propagate as errors from the validation stage. No object
identity or required-field rules are added by the options layer.

## Default-change checklist for `examples/`

Impact of the go-template applier default changing to `missingkey=error`,
verified with `go test ./examples/...`:

| Example | Still renders? | Notes |
|---|---|---|
| `examples/component` | yes | Only `PatchTemplate`s; every key is provided by the test cases or defaulted via `optionsSchema`. The `Fail: parameter missing` case still fails in schema validation, before rendering. |
| `examples/patchbuilder` | yes | Build-time templates already ran with `missingkey=error`; apply-time `PatchTemplate`s are fully keyed by the test options plus schema defaults. |
| `examples/templatebuilder` | yes | `ObjectTemplate` uses `{{.Namespace}}`; every test case supplies `Namespace`. |
| `examples/templatefiles` | yes | Same as `templatebuilder`. |
| `examples/cluster` | yes (unaffected) | No `ObjectTemplate`/`PatchTemplate` objects. The `{{...}}` markers in `kubeproxy/kubeproxy.yaml` are inert salt-style placeholders inside a plain object file and are never template-evaluated. |
| `examples/cluster/options` | yes (unaffected) | Contains a `ClusterOptions` CRD instance, no templates. |

One unit test changed meaningfully: `TestMissingValueOK` in
`pkg/options/gotmpl` previously relied on the lenient default; it now passes
`WithGoTmplOptions(options.MissingKeyDefault)` explicitly. A new
`TestMissingValueErrorDefault` pins the new default.
