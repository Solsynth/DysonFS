# SnFS (web)

The built-in web client for the Solar Network File System. A desktop-like file
manager built with Vue 3, Pinia, Vue Router, Tailwind 4 and daisyUI, styled as
the quiet sibling of the Solar Network console (IBM Plex, ink ground,
periwinkle signal).

## Requirements

- [Bun](https://bun.sh/) (the repository's lockfile is `bun.lock`)
- A running DysonFS API

## Develop

```sh
bun install
bun run dev
```

The dev server proxies `/api` and `/health` to `http://localhost:8080`. Point it
elsewhere when the API runs on another port:

```sh
DYSON_API_TARGET=http://localhost:8081 bun run dev
```

Because the session cookie is `HttpOnly`, the browser must reach both the app
and the API on the same origin. In development that means going through the
Vite proxy rather than calling the API host directly.

## Signing in

The drive has no login form. Signing in opens the identity console's shared page
(`https://id.solian.app/sign-in?redirectUri=…`), which sets the `AuthToken`
cookie for the whole `solian.app` domain and hands the browser back to the
drive. Point `VITE_AUTH_URL` at another page when the identity host differs:

```sh
VITE_AUTH_URL=http://localhost:8080/sign-in bun run dev
```

That only works when the page and the drive share the cookie's domain — an
unshared host signs the person in and then drops them on a signed-out drive.

## Scripts

| Script               | Purpose                                            |
| -------------------- | -------------------------------------------------- |
| `bun run dev`        | Vite dev server with API proxy and HMR             |
| `bun run build`      | Type-check then build the production SPA to `dist` |
| `bun run preview`    | Serve the production build locally                 |
| `bun run type-check` | `vue-tsc` project build                            |
| `bun run lint`       | oxlint + ESLint (both with `--fix`)                |
| `bun run format`     | oxfmt the `src` tree                               |
| `bun run test:unit`  | Vitest (add `--run` for CI)                        |

## Deploying

`bun run build` produces `web/dist`. The master serves that directory at `/`
(`web.staticDir`, default `web/dist`) with a history fallback to `index.html`,
so the SPA and the API share an origin and the session cookie stays
first-party. `make start` from the repo root builds both and runs the master;
the Docker image does the same in a Bun build stage.

## Layout

```
src/
  assets/main.css        design tokens and component classes
  components/            app shell, sidebar, file table/grid, inspector, dialogs
  lib/                   API client, formatting, file kinds, sample fixtures
  stores/                session, vault (drive state), upload, ui, notice
  views/                 DriveView, StorageView
```
