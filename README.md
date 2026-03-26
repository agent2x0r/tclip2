# tclip2

Converts macOS `.textClipping` files to plain text or HTML.

## Install

```sh
go install github.com/agent2x0r/tclip2@latest
```

Or build from source:

```sh
go build -o tclip2 .
```

## Usage

```
tclip2 [-html] [-txt] [-out dir] file.textClipping [...]
```

- `-txt` — output plain text (default)
- `-html` — output HTML
- `-out dir` — write output to `dir` instead of the source file's directory

## How it works

`.textClipping` files can store text in two ways:

1. **Data fork** — a binary plist containing a `UTI-Data` dictionary with typed entries (`public.utf8-plain-text`, `public.html`, etc.). This is the modern format.
2. **Resource fork** — Mac resource fork with `TEXT`, `utf8`, `HTML`, or `utxt` resources. This is the legacy format.

tclip2 tries the data fork first, then falls back to the resource fork.

## License

MIT
