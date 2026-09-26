# Forward-streaming JSONPath profile

The `jsonpath` language implements a restricted profile of [RFC 9535](https://www.rfc-editor.org/rfc/rfc9535.html). Supported selectors use forward traversal and bounded filter candidates. Unsupported constructs return `UNSUPPORTED_QUERY_FEATURE`; malformed expressions return `QUERY_SYNTAX_ERROR`.

## Supported selectors

| Construct | Examples | Contract |
| --- | --- | --- |
| Root | `$` | Document root or virtual record array. |
| Child name | `$.orders`, `$['order id']` | One shorthand or quoted name. |
| Index | `$[0]` | Nonnegative integer. |
| Wildcard | `$.orders[*]`, `$.*` | Children of the current node. |
| Forward slice | `$[1:10:2]`, `$[:10]`, `$[2:]` | Nonnegative bounds and positive step. |
| Filter | `$.orders[?@.total > 100].id` | One bounded candidate at a time. |

Compose at most 64 selectors. Every index, slice bound, slice step, and filter-path index must be in the integer domain `0..9007199254740991`; a slice step must be positive. Execution increments are overflow-safe.

Space, tab, LF, and CR may appear before a segment, including segments in filter
paths: `$ .orders [0] .id` and `$[?@ .active] .id`. A complete query starts with
`$` and ends with its last selector, or with `$` when selecting the root.

Names and string literals may use single or double quotes. JSON escapes apply,
with quote escapes matching the delimiter: `\'` in single-quoted strings and
`\"` in double-quoted strings. Unicode must be well formed. Commas inside
quoted names are literal characters.

## Filter semantics

Filters support current-node singular paths (`@`, `@.name`, `@['name']`, `@[0]`), existence tests, scalar JSON literals, comparison operators, `!`, `&&`, `||`, and parentheses. A filter has at most 128 predicate nodes, 64 parenthesized groups, and 64 segments in each singular path. Evaluation checks cancellation between predicate and child steps.

Use `!@.deleted` to test for an absent member and `!(@.total > 100)` to negate a
comparison. Each nested negation needs parentheses, as in `!(!@.deleted)`.

Numbers are compared as exact JSON decimals, retaining integer and exponent precision. Results retain JSON numeric literals on the wire; clients must preserve precision when decoding them. Strings use lexical Unicode ordering. Boolean and null values support equality. Different JSON types compare unequal; they have no less-than ordering. `!=` is the complement of equality. `<=` and `>=` include equality.

An absent member is distinct from JSON `null`. Two absent singular values compare equal; one absent value and one present value compare unequal. Therefore all of these select the empty object in `[{}]`:

```text
$[?@.missing == @.other]
$[?@.missing != 1]
$[?@.missing <= @.other]
```

Deep comparison of two objects or two arrays is unsupported. Comparing an object/array with a different type yields the corresponding unequal/incomparable result in either operand order.

An existence test succeeds when the singular path resolves, including a resolved `null` or `false` value. An explicit equality comparison tests the value itself.

## Ordering and rejected operations

Array selection preserves the supported selector's order. Streamed object members use file order. Object children selected inside an in-memory filter candidate use sorted keys for deterministic pagination; RFC 9535 leaves object-member ordering unspecified.

Descendant selectors (`$..id`, `$..*`) return `UNSUPPORTED_QUERY_FEATURE`.

Other rejected operations are negative indexes, negative/reverse/zero-step slices, multi-selector unions, root references inside filters, functions such as `length()` and `match()`, and deep same-kind object/array comparison. These features are outside the bounded forward profile.

Use an exact JSON Pointer when the target location is known.

## Virtual record arrays

JSONL and JSON-sequence records have zero-based array indexes. Repeated RS bytes do not increment the record index.

```text
$[*].id
$[?@.total >= 100].id
$[1:100:2]
```
