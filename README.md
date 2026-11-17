# scry

A desktop front end for [scrcpy](https://github.com/Genymobile/scrcpy), built on giokit.

```sh
go run .          # needs adb and scrcpy on PATH
go test ./...     # headless; SCRY_SHOTS=dir saves screenshots
```

## Layout

- `scrcpy/`: `Options` (a session as plain data), `Args`, and `Start` to run it.
- `adb/`: device listing.
- `ui/`: the giokit front end. `settings.go` is the schema (sections, controls,
  which field each edits); `view.go` draws it. Nothing else imports `ui`, so it
  can be rewritten without touching the rest.
- `docs/options.md`: every scrcpy 5.0 flag, categorised, with the rules between them.
  `docs/samples/`: real `--list-*` output to write parsers against.
