# HCL expressions in TOML

AACT uses HashiCorp Configuration Language (HCL) expressions and templates in
selected string values inside its existing TOML files. TOML remains the outer
format: tables, arrays, booleans, numbers, and strings are still written as
TOML. A string containing `${...}` is evaluated as HCL when AACT loads or
resolves that TOML-authored value.

## Whole expressions and templates

When the complete string is one expression, its HCL type is kept. This lets a
TOML field that expects a Boolean, number, or list receive that value directly:

```toml
enabled = "${ true }"                 # Boolean true
timeout_seconds = "${ 30 + 15 }"    # Number 45
arguments = "${ [\"--safe\", \"--read-only\"] }" # List of strings
```

The TOML quoting rules still apply around the expression. To put HCL's quoted
strings inside a TOML basic string, escape their quotation marks as shown in
`arguments` above. Alternatively, TOML literal strings can be useful when the
value does not itself need a TOML escape:

```toml
arguments = '${ ["--safe", "--read-only"] }'
```

If ordinary text surrounds an interpolation, HCL template rules apply and the
result is a string:

```toml
label = "service-${ lower(inputs.name) }"
```

Use `$${` to write a literal `${` in an HCL-evaluated template, for example
`"Write $${name} to the file"` produces `Write ${name} to the file`.

## Available functions

AACT makes this fixed set of HCL functions available:

- `base64encode(string)` and `base64decode(string)`
- `lower(string)`, `upper(string)`, and `trimspace(string)`
- `trimprefix(string, prefix)`, `trimsuffix(string, suffix)`, and
  `replace(string, substring, replacement)`
- `tostring(value)`, `tonumber(value)`, and `tobool(value)`

Other HCL functions and environment lookups are unavailable. Expressions do not
run shell commands or load files.

## Runtime inputs in a package manifest

Runtime expressions can read the final resolved `inputs` object. This is the
same ordinary input map AACT has already resolved using its existing order:
capability defaults, pack defaults, profile values, saved editable answers,
then explicitly submitted values. Profile `fixed` policy continues to lock a
value against later overrides; `default` policy supplies an editable value.
Expressions do not introduce a separate input store or change that ordering.

For example, this package manifest uses username and token inputs to create a
Basic authorization header. The inner HCL string's quote marks are escaped for
the outer TOML basic string:

```toml
[[inputs]]
name = "username"
type = "string"

[[inputs]]
name = "token"
type = "secret"

[mcp]
name = "example-service"
token_input = "token"
token_header = "${ base64encode(\"${inputs.username}:${inputs.token}\") }"
```

The whole expression returns a string. AACT evaluates it after resolving the
selected profile's inputs and passes the resulting value through its existing
validation and MCP configuration path. Values entered or restored as inputs
are data: if a user-supplied token itself contains `${not_expression}`, AACT
does not parse that submitted string a second time.

Whole-expression types can also feed declared typed fields, subject to their
usual schema and validation. For example, `timeout_seconds = "${ 45 }"` is a
number, `enabled = "${ inputs.enable_service }"` is a Boolean when that input is
a Boolean, and `args = "${ inputs.arguments }"` retains a list when the input
is a list. A mixed template such as `"timeout-${ inputs.timeout_seconds }"`
always produces text, so it is not suitable for a numeric field.

## Static document values and profiles

Static values in a package manifest can refer to other top-level values in
that same document. For example, `name = "${ upper(package_id) }"` can read a
top-level `package_id` declared in that `package.toml`. Pack catalog/default
expressions can read top-level values from their own `aact.toml`. Imported
catalog declarations are evaluated in the imported file's context, not the
importer's context.

Profile TOML can use resolved inputs in its values as well. It is also
reevaluated with the profile's own top-level document context. Relative paths
resulting from profile expressions are resolved relative to the profile TOML;
manifest paths use the directory of the TOML file that declares them. Input
values continue through the existing type validation and path normalization.

For example, a profile can define a generic namespace and derive another
editable input from it:

```toml
[inputs]
namespace = "example.test"
service_url = "https://${ inputs.namespace }"
```

## Errors

An invalid expression reports its source TOML file and value path, followed by
HCL's diagnostic. For example, a missing input reference can look like:

```text
package.toml: mcp.actions.authenticate.command[1]: ... unsupported attribute ...
```

Malformed HCL, unknown names/functions, wrong function argument types, and
invalid Base64 input likewise include the source and value path. Correct the
expression at that TOML location; the subsequent package/profile validation
continues to report ordinary schema, type, and input validation errors.

HCL expressions are evaluated only in TOML-authored values. Saved and submitted
input strings remain ordinary values even when they contain `${...}` text.
