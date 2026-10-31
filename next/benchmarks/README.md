# Build measurements

The current experiment uses a read-only snapshot of the local Zeus test database.
Its migration digest matched the current migration directory before inspection.
No application data was read or written. Private schema documents and generated
models remain outside this repository; only aggregate results are recorded.

The input contains 301 logical objects, 3,020 columns and 775 foreign keys. It
includes the audit-reason table and all 196 incoming foreign keys. The consumer
performs a full-row typed read for each of the 300 tables shared with the existing
generated layer. Both directions of FK traversal remain available independently
of layout. Descriptor packages are generated, but unused descriptors are not
imported by the read consumer.

## Real schema result

| Metric | Existing layer | Default flat layout | Optional table layout |
| --- | ---: | ---: | ---: |
| Generated Go bytes | 15,178,110 | 2,077,629 | 1,980,889 |
| Generated Go files, including descriptors | 1,511 | 1,080 | 1,080 |
| Successful clean build | No | 17.63 s | 48.54 s |
| Warm build | — | 0.18 s | 0.22 s |
| Application edit | — | 2.68 s | 4.30 s |
| Executable bytes | — | 6,446,603 | 6,579,619 |
| Clean maximum process RSS | 1,086,096 KiB before failure | 672,848 KiB | 563,720 KiB |
| Clean sampled process-tree RSS | Not captured reliably in first run | 705,112 KiB | 717,780 KiB |

The existing layer exited unsuccessfully in both default and serial package
compilation: after 396.51 seconds by default and 465.59 seconds with `-p 1`.
No compiler diagnostics were captured. The failure cause was not established, so
it is not labeled an OOM. These are censored build attempts, not successful
baseline timings. No real-schema baseline executable-size ratio is claimed.

The default flat output needs approximately 86% less generated Go than the
existing layer while retaining the previously excluded table and FK metadata.
Its lower package count improved the whole-schema consumer's clean and app-edit
builds. Table layout has a smaller largest compiler process and offers narrower
imports and schema-change caching for small services. Neither layout drops
relationships or forces callers into one-way navigation.

These are individual runs on a shared Linux host with Go 1.27.3, not a statistical
performance guarantee. A clean build uses an empty private Go cache and includes
standard-library compilation. The baseline uses the supplied workspace's actual
pinned fork, not the new runtime. The consumer is built but never executed.
Functions are separated per table, so one artificial giant Go function does not
dominate the compiler result. Binaries retain the typed read paths.

GNU time reports the largest process's maximum RSS. The sampler also sums build
descendants across every thread's child list; summed RSS can count shared pages
more than once and is not unique physical memory. Generation byte counts include
all Go descriptor files; compilation includes only reachable packages.

Machine-readable data is in [real-results.json](real-results.json).

## Reproduce

Generate the real schema into private output twice using the same `-import` path:

```sh
go run ./next/cmd/bobgen-db -schemas public,reference,audit,scrape \
  -out /tmp/flat/models -import example.org/real/models -relations all -layout flat
go run ./next/cmd/bobgen -schema /tmp/flat/models/schema.json \
  -out /tmp/tables/models -import example.org/real/models -relations all -layout tables
```

Then use the supplied existing generated models and workspace:

```sh
python next/benchmarks/real_schema.py \
  --models /tmp/flat/models \
  --legacy-models /path/to/existing/bobmodels \
  --legacy-workspace /path/to/existing/go.work
```

`--only compact` profiles a layout without repeating the expensive baseline.
`--parallelism 1` controls package build concurrency for both variants. The script
creates separate temporary consumers, workspaces and caches; it changes no source
service, source module, original workspace or database. `go-bindings.json` supplies
actual generated import paths and symbol names for either layout.

The earlier 150-table synthetic fixture remains reproducible with:

```sh
python next/benchmarks/compare.py --tables 150 --columns 16
go test ./next -run '^$' -bench BenchmarkBuild -benchmem
```

Its initial historical data is [sample-results.json](sample-results.json). That
small single-table consumer did not show compiler-memory improvement and should
not be substituted for the real-schema experiment. The native introspection and
value layer has changed since that initial measurement.
