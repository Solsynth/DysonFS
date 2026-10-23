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

## File picker for other apps

`/picker` is a second page of this build: a file browser an app embeds in a
frame to let a person pick a file without leaving that app. The drive's session
cookie stays with the drive, so the host page never handles a token or proxies
file bytes.

```html
<script src="https://fs.solian.app/picker/snfs-picker.js"></script>
<script>
  const files = await openSnfsPicker(
    { frameUrl: 'https://fs.solian.app/picker', dialogTitle: 'Attach a file' },
    { view: 'files', multiple: true, mimeTypes: ['image/*'], upload: true },
  )
  // files[0].url is same-origin with the drive and carries the cookie
</script>
```

`openSnfsPicker` resolves with an array: the chosen `PickerFile`s, or an empty
array when the person cancelled. `frameUrl` is the only required config.

### Options

| Option          | Type                                   | Meaning                                          |
| --------------- | -------------------------------------- | ------------------------------------------------ |
| `view`          | `'files' \| 'folders' \| 'any'`        | What may be chosen. Default `files`.             |
| `multiple`      | `boolean`                              | Allow more than one. Default `false`.            |
| `mimeTypes`     | `string[]`                             | e.g. `['image/*', 'application/pdf']`.           |
| `kinds`         | `string[]`                             | `image`, `video`, `audio`, `document`, `data`, … |
| `extensions`    | `string[]`                             | e.g. `['png', 'jpg']`.                           |
| `startFolderId` | `string \| null`                        | Folder to open; `null` is the drive root.        |
| `upload`        | `boolean`                              | Offer an upload control and a drop target.       |
| `title`         | `string`                               | Heading inside the frame.                        |
| `description`   | `string`                               | One line under the heading.                      |
| `theme`         | `'auto' \| 'dark' \| 'light'`          | Frame theme. Default `auto`.                     |
| `locale`        | `'en' \| 'zh-CN'`                      | Frame language.                                  |

The second argument may also be `{ options, token }` when the host is on
another site (see *Cross-site embeds* below).

Folders stay navigable even when `view` is `files`: a file that the options
refuse is drawn muted with the reason, so an empty folder can explain itself.

### Uploads are unindexed

With `upload: true` the panel accepts a drop or a file dialog and posts the new
contents with `index=false`, which keeps the row out of the drive's folder tree
(`files/unindexed`) while giving it a stable id and URL. A picker should not
copy every attachment into the person's drive as a side effect. The new row
arrives already selected and is handed back on confirm.

### Frame protocol

The SDK is a convenience over two messages, both tagged
`{ channel: 'snfs-picker' }` and both restricted to the drive's origin:

| Direction | Message                                       | Effect                          |
| --------- | --------------------------------------------- | ------------------------------- |
| host →    | `{ type: 'picker:open', options }`            | Start (or restart) a request.   |
| host →    | `{ type: 'picker:close' }`                    | Dismiss.                        |
| frame →   | `{ type: 'picker:ready' }`                    | Frame mounted.                  |
| frame →   | `{ type: 'picker:signed-out' }`               | Person must sign in first.      |
| frame →   | `{ type: 'picker:action', action, files? }`   | `picked` with files, or `cancelled`. |

The frame answers only to the origin in its referrer, and only to messages from
`window.parent`, so a page that frames it without being the host cannot read a
selection. `/picker?view=…&mime=…&multiple=1` opens the same surface with the
request already in the URL, for a host that would rather not script anything.
`/picker-demo.html` in a dev build is a working example of all of it.

### Cross-site embeds

DysonFS does not set `AuthToken`; Stargate sets it with `Path=/`,
`SameSite=Lax` on `[auth] cookieDomain`. Browsers refuse `SameSite=Lax` cookies
in a cross-site frame, so a host on another site cannot authenticate the frame
with the cookie. Hand the frame a bearer token instead:

```js
const files = await openSnfsPicker(
  { frameUrl: 'https://fs.solian.app/picker' },
  { options: { view: 'files', mimeTypes: ['image/*'] }, token: accessToken },
)
```

The token travels in the `picker:open` message — never in the frame URL — and
the frame sends it as `Authorization: Bearer` on every API call, uploads
included. A same-site host omits it and keeps using the cookie.

The frame reports `picker:signed-out` only after a short grace period, so a
token host that answers a fraction late is not torn down as anonymous.

## Layout

```
index.html               the drive
picker.html              the picker frame a host embeds
picker-demo.html         a host page that shows the embed working
src/
  assets/main.css        design tokens and component classes
  components/            app shell, sidebar, file table/grid, inspector, dialogs
  components/picker/     the picker panel and its compact session gate
  lib/                   API client, formatting, file kinds
  lib/picker/            frame protocol, acceptance rules, embed SDK
  picker/                the picker page's app and entry
  stores/                session, vault (drive state), upload, ui, notice
  views/                 DriveView, StorageView
```
