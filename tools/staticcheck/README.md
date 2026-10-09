# Staticcheck build dependencies

Go 1.27.2 emits export data format 5. Staticcheck v0.8.1's original
`golang.org/x/tools` dependency only reads
format 4. This module builds the same analyzer with the format-5 reader from
`x/tools v0.50.0`, which PSP already uses. It does not change PSP's runtime graph.

From the repository root, use the compiler selected by the root `go.mod`:

```sh
go -C tools/staticcheck build -o /tmp/psp-staticcheck honnef.co/go/tools/cmd/staticcheck
/tmp/psp-staticcheck -tags node_reinstall_acceptance ./...
```

The workflow hashes this module's pins and checksums in both its build cache and
the shared module cache. When a released Staticcheck includes a compatible
export reader, update these pins and run the full analysis before removing the
separate module.
