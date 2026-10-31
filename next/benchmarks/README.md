# Build experiment

```sh
python next/benchmarks/compare.py --tables 150 --columns 16
go test ./next -run '^$' -bench BenchmarkBuild -benchmem
```

The script compiles an offline schema fixture generator, generates both variants,
and builds a consumer that selects full rows from the referenced owner table.
The fixture contains one owner and 149 referencing tables, each with 16 columns.
It uses the existing fork as the baseline, including its table-package split and
forward-only relationship policy. Factories and slice mutations are disabled in
the baseline, matching the production configuration's slice-mutation setting.

Every variant gets its own empty Go build cache. It measures clean build, unchanged
build, an application comment edit, and adding one field to an unrelated child
table. The last case already benefits from the baseline's table-package split;
it is not evidence of a newly fixed baseline dependency problem. All generated Go
files count toward source size, but only the packages reachable by the consumer
are compiled. No database or driver is included in the consumer executable.

The compact output has fewer generated conveniences. This compares an architecture
and one equivalent read operation, not feature-equivalent libraries or complete
Zeus services. Do not extrapolate these numbers to the production schema. Cold
build costs include the standard library. GNU time's maximum process RSS is the
largest process's peak, not total concurrent compiler memory. The script records
errors rather than turning a failed baseline into a performance comparison.

The checked-in sample is one run on a shared Linux host, with Go 1.27.3. Build
timings are directional, not a statistical performance claim. The peak compiler
memory result did not improve. Use repeated isolated runs and real service
consumers before setting performance guarantees.

| Metric | Existing fork | Compact design |
| --- | ---: | ---: |
| Generated Go files | 750 | 150 |
| Generated Go bytes | 7,079,248 | 915,896 |
| Clean build | 10.99 s | 7.88 s |
| Warm build | 0.10 s | 0.08 s |
| Application edit | 0.99 s | 0.48 s |
| Unrelated schema edit | 0.10 s | 0.08 s |
| Executable bytes | 4,297,716 | 3,332,036 |
| Clean maximum process RSS | 282,032 KiB | 282,972 KiB |

Machine-readable results are in [sample-results.json](sample-results.json).
