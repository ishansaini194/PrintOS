# Running PrintOS cloud (test-only, single droplet)

Brings up a dedicated PrintOS Postgres + the cloud backend via docker-compose,
bound to **localhost only**. Assumes Docker + Compose are installed and you are
in the repo root (`/opt/PrintOS`).

> Scope: test-only. Nothing is exposed publicly. Does not touch `lms-db-1`
> (5433) or `customurls_postgres_1`.

## (a) Create `.env` and paste keys

```bash
cp .env.example .env
```

Then edit `.env` and set the secrets (everything else has working defaults, and
compose supplies the DB + PDF-dir values for you):

- `PRINTOS_ADMIN_KEY=` → a long random string (required for `/admin` routes)
- `RAZORPAY_KEY_ID=` / `RAZORPAY_KEY_SECRET=` → your Razorpay **test** keys

Leave `RAZORPAY_BASE_URL` unset (targets the real Razorpay API).

## (b) Build the image

```bash
docker compose build
```

## (c) Start the database (and wait for healthy)

```bash
docker compose up -d db
docker compose ps        # wait until db shows "healthy"
```

## (d) Apply migrations 0001–0005

No separate step. The cloud applies `migrations/*.sql` in order at startup via
its built-in runner (tracked in `schema_migrations`, idempotent). Starting the
cloud in step (e) runs them. To confirm afterwards:

```bash
# tables created by the migrations
docker compose exec db psql -U printos -d printos -c "\dt"
# which migration files have been applied
docker compose exec db psql -U printos -d printos -c "SELECT filename FROM schema_migrations ORDER BY filename;"
```

(You can also reach the DB by hand from the droplet on the localhost-only port:
`psql -h 127.0.0.1 -p 5434 -U printos -d printos`.)

## (e) Start the cloud

```bash
docker compose up -d cloud
docker compose logs -f cloud     # look for "migrations applied" then "PrintOS cloud starting"
```

## (f) Confirm it's listening on localhost:8080

```bash
curl -s http://localhost:8080/health
# -> {"protocol":"1.0.0","status":"ok"}
```

Verify the normalization tools are callable **inside** the cloud container:

```bash
docker compose exec cloud soffice --version
docker compose exec cloud convert --version
docker compose exec cloud heif-convert --version
docker compose exec cloud pdfinfo -v
```

## Teardown

```bash
docker compose down          # stop containers, keep data volumes
docker compose down -v       # also delete the printos-db-data / printos-pdfs volumes
```

## Known follow-up — ImageMagick PDF policy

Ubuntu's ImageMagick ships a policy that can block PDF read/write. If an
image→PDF upload test (`jpg`/`heic`) fails with `convert: not authorized ...
PDF`, uncomment the `sed` line in the `Dockerfile` (runtime stage) and rebuild:

```bash
docker compose build cloud && docker compose up -d cloud
```
