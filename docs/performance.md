# Performance and supported workload shape

## Reproducible local measurements

Measured September 7, 2026 on Linux/amd64, Go 1.27.1, AMD EPYC 9V74, `GOMAXPROCS=4`. These are short, single-host in-memory-reader benchmarks; disk throughput, storage latency, production RSS, and ARM performance were not measured.

Command:

```sh
go test ./internal/engine -run='^$' -bench='Benchmark(RejectedLongPath|PaginationPositions)' -benchmem -benchtime=100ms
```

| Scenario | Time/op | Allocated bytes/op | Allocations/op |
| --- | ---: | ---: | ---: |
| Reject 10-KiB key under 1-KiB result cap | 122,098 ns | 87,950 | 38 |
| First 100 matches of 10,000 records | 62,044 ns | 77,888 | 939 |
| 100 matches after skipping 5,000 | 1,945,681 ns | 815,063 | 31,042 |
| Final 100 after skipping 9,900 | 3,447,912 ns | 1,530,328 | 60,436 |

Bytes/op measures total allocation traffic during one operation. Measuring peak
live memory requires heap profiling. Run the command above to measure the
current implementation on a comparable machine.

## Pagination complexity

With uniformly distributed matches, page size `P`, and `N` consumed matches, prefix rescanning visits approximately `N² / (2P)` match positions across the full result. One million matches in 1,000-item pages therefore imply roughly 500.5 million prefix-match visits, plus lookahead and structural work. This is an algorithmic calculation, not a throughput measurement.

Use known Pointers or selective JSONPath expressions to extract compact facts. Benchmark first, middle, and final pages for a representative document before using long pagination chains. Full-file export and global aggregation are outside this server's bounded query interface.

## Memory and latency

Memory use depends on token size, nesting, retained keys, filter candidates,
results and concurrent scans. A single large string, record or filter candidate
can exceed a limit even when the overall file is small.

Filter candidates temporarily exist as encoded bytes and Go values. Parser namespaces and maps also incur bookkeeping overhead. The maximum number of scans limits simultaneous active parsers; suspended cursors contain no parser stack or retained candidate. Use OS resource controls for a hard process memory ceiling.

The request deadline includes admission and parsing. Local filesystem calls are not universally interruptible. The source root should be on responsive local storage, and callers should handle timeout and busy-source errors.

## Validation coverage

A probe validates only its reported prefix/records. A nonterminal page stops after a lookahead match, so subsequent bytes may still contain errors. A terminal successful page completes input validation. Source fingerprint checks run after successful execution, including terminal pages.
