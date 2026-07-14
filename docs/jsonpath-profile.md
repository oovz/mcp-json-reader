# Forward-streaming JSONPath profile

The `jsonpath` query language is based on RFC 9535 but deliberately limited to operations that can be executed with bounded forward traversal or a bounded candidate.

Unsupported constructs return `UNSUPPORTED_QUERY_FEATURE`; malformed expressions return `QUERY_SYNTAX_ERROR`.

## Supported selectors

| Construct | Example | Notes |
| --- | --- | --- |
| Root | `$` | Selects the document root or virtual record array |
| Child shorthand | `$.orders` | RFC shorthand names, including Unicode names |
| Quoted child | `$['order id']` | One single- or double-quoted member name with JSON escapes; a comma inside the quoted name is literal |
| Non-negative index | `$[0]` | No leading zeros except `0` |
| Wildcard | `$.orders[*]` | File order for streamed objects/arrays |
| Descendant | `$..id`, `$..*` | At most one and it must be the final selector; preorder traversal |
| Forward slice | `$[1:10:2]`, `$[:10]`, `$[2:]` | Non-negative bounds and a positive step |
| Filter | `$.orders[?@.total > 100]` | Evaluated from one bounded candidate |

Up to 64 selectors may be composed:

```text
$.orders[?@.total > 100 && @.active == true].id
```

## Filter expressions

Filters support:

- current-node paths: `@`, `@.name`, `@['name']`, `@[0]`;
- existence tests: `@.name`;
- JSON literals: strings, numbers, `true`, `false`, `null`;
- comparisons: `==`, `!=`, `<`, `<=`, `>`, `>=`;
- boolean operators: `!`, `&&`, `||`;
- parentheses.

Numbers are compared as exact JSON decimals. Large integers and decimal exponents are not converted to `float64`.

A filter is limited to 128 predicate nodes and 64 nested parenthesized groups. Each current-node path is limited to 64 segments. Evaluation checks cancellation and the configured scan deadline between predicate and child steps.

Equality is supported for scalar values. Ordering is defined only for number/number and string/string pairs. Other ordering pairs and scalar type mismatches evaluate to false, following RFC 9535; `<=` and `>=` are also true for equal scalar values, including equal booleans or `null`. Deep object/array equality is outside the profile.

When a filter suffix selects object children from a bounded in-memory candidate, keys are processed in sorted order. RFC 9535 does not specify object-member ordering; sorting makes pagination deterministic.

## Explicitly unsupported

The following require array-length knowledge, global ordering, rescans, or cross-tree state and are rejected:

- negative indexes: `$[-1]`;
- negative bounds or reverse/zero-step slices: `$[::-1]`;
- multi-selector unions that can reorder or duplicate results: `$[2,0]`, `$['a','b']`;
- root references inside filters: `$[?$.limit < @.value]`;
- functions such as `length()`, `count()`, `match()`, `search()`, or `value()`;
- deep object/array comparison;
- more than one descendant selector, which can duplicate or reorder the RFC nodelist;
- any expression combining descendant and filter selectors, which would require delayed cross-depth processing;
- any selector after a descendant, whose RFC segment ordering differs from final-node document preorder;
- more than 64 selectors, which is outside the bounded complexity profile.

Use an exact Pointer when the location is already known. It has a smaller execution state and can stop creating matches after the one target path.

## Virtual arrays

JSONL and JSON-sequence records are addressed as array elements without materializing an array:

```text
record 0 → $[0]
record 1 → $[1]
record 2 → $[2]
```

Examples:

```text
$[*].id
$[?@.total >= 100].id
$[1:100:2]
```
