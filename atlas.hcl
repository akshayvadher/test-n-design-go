// atlas.hcl — declarative atlas environments for the library service.
//
// `local` is the dev/CI environment: it points `url` at the live Postgres the
// service is migrating, and `dev` at an ephemeral postgres:16 container that
// atlas spins up on demand for `atlas migrate hash` and `atlas migrate diff`.
// The dev-url avoids requiring a hand-managed scratch DB on every developer
// laptop. See https://atlasgo.io/concepts/dev-database for the rationale.
//
// `LIBRARY_DATABASE_URL` must be exported in the shell (or supplied via the
// `.env` file the developer sources) before invoking `task migrate:apply`.

env "local" {
  url = "${LIBRARY_DATABASE_URL}"
  dev = "docker://postgres/16/dev?search_path=public"

  migration {
    dir = "file://migrations"
  }
}
