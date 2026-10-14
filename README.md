# Attendance & Leave Management (Telegram Mini App + HR admin panel)

Employees check in and out with a photo (and optional GPS), request leave and see their
own records in a **Telegram Mini App**. The owner, HR, branch admins and managers confirm
attendance, approve leave, manage staff and run reports in a web **admin panel**. The
company can have **several branches**, each run by one branch admin the owner assigns.
A Go API backs both apps and sends Telegram notifications. Telegram itself (bot token,
Mini App URL, group chats) is configured in the admin panel — not in `.env`.

```
attendance-system/
├── backend/                 Go API (Gin, GORM, MySQL) + scheduler
│   ├── cmd/api/             serve | migrate | create-admin | seed-demo
│   ├── internal/
│   │   ├── auth/            JWT sessions, Mini App initData, RBAC (permissions.go)
│   │   ├── employee/        staff, Telegram link, admin panel logins
│   │   ├── branch/          branches and their branch admins
│   │   ├── attendance/      check-in/out, late/early/hours, review, manual edits
│   │   ├── leave/           requests, approval → marks days as LEAVE
│   │   ├── report/          daily/period/staff reports, Excel/CSV
│   │   ├── scheduler/       missing check-in alert, daily report, day close
│   │   ├── notification/    Telegram notifications (+ notifications table)
│   │   ├── telegram/        send-only Bot API client
│   │   ├── storage/         S3-compatible photo storage (R2 / S3 / MinIO)
│   │   ├── settings/        company settings (incl. Telegram), cached
│   │   ├── secret/          encryption of stored credentials (bot token)
│   │   ├── audit/           append-only audit log
│   │   └── httpapi/         /api/v1 (admin panel) and /api/v1/app (Mini App)
│   └── migrations/          SQL migrations (embedded, applied on start)
├── miniapp/                 Telegram Mini App for employees (Nuxt 3 SPA, /app/)
├── admin/                   HR admin panel (Nuxt 3 SPA, /admin/)
├── ops/nginx/               reverse proxy (+ TLS template)
├── ops/backup/              daily mysqldump with retention
├── docker-compose.yml       self-hosted stack (nginx + one container per app)
├── Dockerfile               single-container image (Railway): API + both front ends
├── railway.json             Railway build/deploy settings
└── .env.example
```

## How it works

```
Employee ──Telegram Mini App──HTTPS /app/ + /api/v1/app (signed initData)──┐
                                                                            ├─▶ nginx ─▶ API ─▶ MySQL
HR / managers ──browser──HTTPS /admin/ + /api/v1 (session cookie)──────────┘              ├─▶ R2/S3 (photos)
                                                                                           └─▶ Telegram (notifications)
```

- **The API is the only place with rules.** Both front ends are static apps; every
  permission check, scope and business rule runs in Go, so they can never disagree.
- **Who is who.** In the Mini App, Telegram signs the user's identity (`initData`); the
  API verifies it with the bot token and maps the numeric Telegram user ID to an
  employee. In the admin panel, people sign in with email + password.
- **There is no chat bot.** The bot account from @BotFather just hosts the Mini App
  (its menu button opens it) and sends notifications. Messages sent *to* the bot are
  not read.
- **Notifications** about a check-in or leave request go, with the photo and a **Review
  in app** button, to the branch's review chat if it has one; otherwise to the company
  review chat if set; otherwise by DM to the employee's manager and branch admin — or,
  when the branch has neither, to HR and the owner. Employees are told when their
  attendance or leave is reviewed. Daily reports go to the company group and to each
  branch's own group.

## Quick start

```bash
cp .env.example .env            # fill in secrets: openssl rand -hex 32
docker compose up -d --build
docker compose logs -f api

# Create the first super admin (password from env, not a flag):
docker compose exec -e ADMIN_PASSWORD='a-long-password' api \
  /app/api create-admin -code GT000 -name "Owner Name" -email owner@company.com
```

Then open `https://<your-domain>/admin/`, sign in, and:

1. **Settings → Telegram:** paste the bot token from @BotFather and the Mini App URL
   (`https://<your-domain>/app/`). The token is checked with Telegram, stored encrypted,
   and the bot's **Open App** menu button is pointed at the Mini App. Until this is done
   the Mini App says it isn't set up and no notifications are sent. Optionally add the
   company group's chat ID for the daily report (how to find it is shown on the page).
2. **Branches:** rename *Main branch* (all existing staff start there) and add the others.
3. **Employees → Add employee** for each person, choosing their branch. Everyone needs
   their numeric **Telegram user ID** for the Mini App: they open the bot, tap **Open App**,
   and the "No access" screen shows their ID. Link your own account too.
4. **Branches → a branch → Assign a new admin**, then create that person's panel login
   (the page links to it).

Roles: `super_admin` (the owner), `hr_admin` (company-wide HR), `branch_admin` (one
branch), `manager`, `staff`. Only the owner creates branches, assigns branch admins,
grants admin roles and creates panel logins. A manager reviews the people whose
**Manager** is set to them.

### HTTPS

Telegram only opens Mini Apps over HTTPS. Pick one:

1. **Host Nginx + Certbot**: set `NGINX_HTTP_PORT=8088` in `.env` and proxy your host
   server block to `http://127.0.0.1:8088`.
2. **Certificates in the stack:** put `fullchain.pem` / `privkey.pem` in
   `ops/nginx/certs/`, rename `ops/nginx/conf.d/ssl.conf.example` to `ssl.conf`.
3. **Cloudflare proxy / tunnel** in front of port 80.

### Photo storage

Photos go to any S3-compatible bucket. For Cloudflare R2:

```
STORAGE_ENDPOINT=<account-id>.r2.cloudflarestorage.com
STORAGE_ACCESS_KEY=...   STORAGE_SECRET_KEY=...
STORAGE_BUCKET=attendance   STORAGE_REGION=auto   STORAGE_USE_SSL=true
```

Create the bucket first and keep it **private** — photos are only served through the
authenticated API, never by public URL. Then remove the `minio` service from
`docker-compose.yml`. (As of October 2026 the `minio/minio` image can no longer be
pulled from Docker Hub, so a fresh `docker compose up` needs R2/S3 or another image.)

## Deploy on Railway

The root `Dockerfile` builds one image in which the API also serves the admin panel
(`/admin/`) and the Mini App (`/app/`), all on one domain. `railway.json` tells Railway
to build it and to wait for `/health` before switching traffic. Railway needs four
services in one project:

1. **+ New → GitHub Repo** → this repository (leave *Root Directory* empty).
2. **+ New → Database → MySQL**, **+ New → Database → Redis**, **+ New → Bucket**
   (photo storage; private, S3-compatible).
3. In the app service: **Variables → Raw Editor**, paste the block below, and set
   `JWT_SECRET` (`openssl rand -hex 32`). The names inside `${{…}}` are the service
   names — change `MySQL`, `Redis` and `Bucket` if yours differ.

   ```
   PORT=8080
   JWT_SECRET=
   MYSQL_HOST=${{MySQL.MYSQLHOST}}
   MYSQL_PORT=${{MySQL.MYSQLPORT}}
   MYSQL_USER=${{MySQL.MYSQLUSER}}
   MYSQL_PASSWORD=${{MySQL.MYSQLPASSWORD}}
   MYSQL_DATABASE=${{MySQL.MYSQLDATABASE}}
   REDIS_URL=${{Redis.REDIS_URL}}
   STORAGE_ENDPOINT=${{Bucket.ENDPOINT}}
   STORAGE_ACCESS_KEY=${{Bucket.ACCESS_KEY_ID}}
   STORAGE_SECRET_KEY=${{Bucket.SECRET_ACCESS_KEY}}
   STORAGE_BUCKET=${{Bucket.BUCKET}}
   STORAGE_REGION=auto
   STORAGE_URL_STYLE=virtual
   CLIENT_IP_HEADER=X-Real-IP
   ```

   Never change `JWT_SECRET` afterwards: it signs sessions and encrypts the bot token
   saved in Settings (a new secret means everyone signs in again and the token must be
   re-entered).
4. **Settings → Networking → Generate Domain**, target port `8080`. Only this one service
   gets a domain: do not add separate services for `admin/`, `miniapp/` or `backend/`
   (each would be on its own domain, where the panel and the Mini App cannot reach the API). When the deploy is
   green, `https://<domain>/health` shows database, redis and storage `connected`.
5. Create the owner from a terminal with the [Railway CLI](https://docs.railway.com/guides/cli)
   (`railway link` to this project and the app service first):

   ```bash
   railway ssh
   ADMIN_PASSWORD='a-long-password' /app/api create-admin -code GT000 -name "Owner Name" -email you@company.com
   ```

6. Open `https://<domain>/admin/` and continue from step 1 after *Quick start* above
   (*Settings → Telegram*, Mini App URL `https://<domain>/app/`).

Notes:

- **URL style:** the bucket's *Credentials* tab says whether it uses virtual-hosted or
  path-style URLs; for a path-style bucket set `STORAGE_URL_STYLE=path`.
- **Client IPs** (login rate limit, audit log): Railway's edge connects from shared CDN
  addresses and puts the visitor's IP in `X-Real-IP`, hence `CLIENT_IP_HEADER`. The
  `ip=` of each request in the deploy logs should match the `srcIp` that
  `railway logs --http` shows for it.
- **Backups:** there is no backup container here — enable backups on the MySQL service
  in Railway. The 08:00 backup alert stays silent because no backup status file exists.
- **Demo data** on a throwaway project only: `railway ssh`, then `/app/api seed-demo -force`.

## Telegram Mini App (employees)

`miniapp/` opens inside Telegram from the bot's menu button. Employees can check in and
out with a live camera photo and optional GPS, see today's status and their monthly
history, request and cancel leave, and view their profile. Managers and admins also get
a **Team** tab to confirm or reject attendance (with the photo), approve leave and see
today's team report — handy on the go; the admin panel has the full tools.

**Setup.** Set the Mini App URL in *Settings → Telegram*; the API points the bot's chat
menu button at it (on save and on every start). Optional: in @BotFather → *Bot Settings →
Configure Mini App*, set the same URL — this enables the bot profile's **Open** button and
the `t.me/<bot>?startapp=team` links used in group-chat notifications. Employees see
their branch on the home screen and profile; a branch admin's **Team** tab covers their
branch.

**Authentication.** Every request sends Telegram's signed `initData` as
`Authorization: tma <initData>`. The API verifies the HMAC with the bot token
(`auth/webapp.go`) and rejects launches older than `MINIAPP_SESSION_TTL` (24h; the user
just reopens the app). `/api/v1/app` exposes only what the app needs; reviewer routes
reuse the admin panel's handlers and permission checks. Panel sessions are not accepted
there, and `initData` is not accepted on the panel's routes.

**Photos.** Taken with a live camera preview (falling back to the device camera picker
where a client's web view has no camera access), downsized to a ~1280px JPEG in the
browser — which also strips EXIF metadata — and uploaded (8 MB cap). The API sniffs the
type and blocks re-use of the same image by SHA-256.

**Location** uses Telegram's LocationManager (Bot API 8.0+) and falls back to browser
geolocation, following the `gps_*` settings.

## Branches

Every staff member, manager and branch admin belongs to one branch; the owner and HR
admins work across all branches. Each branch has **at most one branch admin**, and a
person can admin only one branch. Only the owner assigns them (*Branches → a branch →
Assign a new admin*): the person gets the Branch Admin role and moves to that branch;
a replaced admin becomes staff, and their panel session ends at once. Deactivating a
branch admin leaves the branch without one.

A branch admin has HR's tools limited to their branch: they see and review only their
branch's people, add staff (always into their branch, as staff or manager), correct
attendance, approve leave and export their branch's reports. On **My branch** they can
set the branch's own office location (for check-in GPS; otherwise the company office
applies) and its Telegram chats — a group for its daily report and an optional review
chat. They cannot rename or close the branch, change its admin, or see company settings.

## HR admin panel

`admin/` is served at `/admin/` (the site root redirects there). The menu is grouped:
**Attendance** (Records, Leave requests, Reports), **Schedules** (Weekly roster, Special
schedules, Shifts, Holidays) and **Settings** (General, Audit log). What each role sees:

| Page | Owner (super admin) | HR admin | Branch admin | Manager |
|---|---|---|---|---|
| Dashboard — today's counts, each person's shift, who needs attention | all (branch filter) | all (branch filter) | their branch | their team |
| Attendance › Records — filter, **Edit** on each row, detail with photos/GPS/review history (confirm/reject there) | ✓ | ✓ | their branch | their team |
| ↳ add a record / edit times (reason required, audited) | ✓ | ✓ | their branch | — |
| Attendance › Leave requests — approve/reject | ✓ | ✓ | their branch | their team |
| Schedules › Weekly roster — shift per person per day, copy last week, Excel/CSV export | edit | edit | edit their branch | view team |
| Schedules › Special schedules — date-range overrides (another shift, custom hours, days off) | ✓ | ✓ | their branch | view team |
| Schedules › Shifts — hours, grace, work days, normal or cross-day (night) | edit | edit | view | view |
| Schedules › Holidays | edit | view | view | view |
| Employees — add, edit, link Telegram, set branch/manager/role, deactivate | ✓ | ✓ (not admins) | their branch | view team |
| ↳ create panel logins, grant admin roles | ✓ | — | — | — |
| Branches — add/edit/close branches, assign branch admins | ✓ | — | My branch: office + Telegram | — |
| Reports — period summary per employee, Excel/CSV export | ✓ | ✓ | their branch | their team |
| Settings › General — Telegram, company, GPS, scheduled jobs | edit | view | — | — |
| Audit log | ✓ | — | — | — |

**Schedules.** What someone works on a date is decided in one place
(`attendance.Planner`): a **special schedule** covering the date wins, then the **weekly
roster** entry for that date, then their **usual shift** on its work days. Holidays and
approved leave still apply on top. Shifts are *normal* (end the same day, 08:00–17:00)
or *cross-day* (a night shift ending the next morning, 22:00–06:00 "+1").

**Night shifts** count for the day they start: a 22:00 check-in and the 06:05 check-out
the next morning are one record on the first day. The Mini App keeps showing that shift
(with **Check out**) after midnight; a check-in after midnight (e.g. 00:30) is late for
last night's shift, unless today's own shift starts within two hours. A check-out still
counts for the night shift up to 8 hours after it ends. The 10:00 missing check-in alert
and the day close skip anyone whose shift has not started yet. In a manual edit, a
check-out at or before the check-in on a cross-day shift is read as the next morning.

**Sessions.** Signing in sets an `HttpOnly`, `SameSite=Strict` cookie (`Secure` in
production) — the page's JavaScript never sees the token, and nothing is kept in
`localStorage`. Cookie-authenticated writes must also send `X-Requested-With` (CSRF
defence); the panel's API client does. Sessions last `JWT_TTL` (12h); deactivating an
employee or changing their role takes effect on their next request. API clients can
still use `Authorization: Bearer <token>` from the login response.

## Development

Run the API on your computer against a local MySQL (e.g. MAMP):

```bash
# root .env — where MySQL is:
#   MYSQL_HOST=127.0.0.1   MYSQL_PORT=3306   MYSQL_DATABASE=attendance
#   MYSQL_USER=root        MYSQL_PASSWORD=<your MySQL password>
docker compose -f docker-compose.dev.yml up -d   # Redis + S3 photo storage on 127.0.0.1
cp backend/.env.example backend/.env             # Mac-only settings (Redis, storage, APP_ENV)
cd backend
go run ./cmd/api migrate                         # creates the database if needed, applies migrations
go run ./cmd/api serve                           # http://127.0.0.1:8080

# first owner account for the admin panel (once):
ADMIN_PASSWORD='a-long-password' go run ./cmd/api create-admin -code GT000 -name "Owner Name" -email you@company.com
```

Run from `backend/`, the API reads `backend/.env` and then the root `.env`; variables
set in your shell win. No MySQL on your computer? `docker compose -f docker-compose.dev.yml
--profile mysql up -d` also starts one on `127.0.0.1:3307` (user and password
`attendance`); set `MYSQL_PORT=3307`, `MYSQL_USER=attendance`, `MYSQL_PASSWORD=attendance`.
Stop the dev services with `docker compose -f docker-compose.dev.yml down`.

**On a Docker server** the same keys point at the bundled MySQL container:
`MYSQL_HOST=database`, `MYSQL_PORT=3306`, and a non-root `MYSQL_USER` (the MySQL image
reserves `root` for `MYSQL_ROOT_PASSWORD`) — see `.env.example`.

Front ends (each proxies `/api` to `API_DEV_TARGET`, default `http://localhost:8080/api`):

```bash
cd admin && npm ci && npm run dev     # http://localhost:3001/admin/
cd miniapp && npm ci && npm run dev   # http://localhost:3000/app/ (outside Telegram it shows "Open in Telegram")
```

To try the Mini App on a phone, expose its dev server over HTTPS (e.g. `cloudflared
tunnel`) and set that as the Mini App URL of a test bot in *Settings → Telegram*.
Both apps need a current browser / web view (Chrome 111+, iOS 16.4+).

### Demo data

To explore the admin panel without real staff, fill a development database with sample
data (from `backend/`):

```bash
go run ./cmd/api seed-demo
```

It adds three branches (Main branch, Siem Reap, Battambang) with a branch admin each, an
HR admin, two managers, 17 employees and four weeks of attendance up to today — on time,
late, early leave, absent and missing check-out days, with photos, GPS points and
reviews (some still pending, one rejected) — plus approved, pending, rejected and
cancelled leave requests, Cambodia's fixed-date public holidays, a morning and a night
(cross-day) shift, a few weekly roster days and three special schedules. The command prints
the admin panel logins: all of them use the password `Demo@12345` (set `DEMO_PASSWORD`
to choose another). If the database already has a super admin, that owner is kept and
no demo owner is created.

Demo employees have codes starting with `D-` and placeholder Telegram IDs
(9900000001…), so they cannot open the Mini App; link your own Telegram ID to one of
them in the admin panel to try it. Running the command again does nothing. It refuses to
run with `APP_ENV=production` — never seed a live database. To start over, drop the
database and run `migrate` and `seed-demo` again.

## Business rules (as implemented)

| # | Rule | Where |
|---|---|---|
| 1–3 | One check-in and one check-out per day; no check-out without check-in | `attendance/service.go` (row lock + unique `(employee_id, work_date)`) |
| — | A **rejected** check-in/out can be re-submitted with a new photo; history is kept (old review marked `SUPERSEDED`) | same |
| 4–5 | Photo required; the same image can't be reused | `attendance/service.go` (SHA-256, migration `0002`) |
| 6 | GPS: `gps_enabled`, `gps_required`, `gps_reject_outside`, office lat/lng/radius | `company_settings` |
| 7 | `confirmation_required=false` auto-confirms | `company_settings` |
| 8–10 | Staff: self · Manager: assigned team (`manager_id`) · HR/Super admin: all. No self-review except super admin | `auth/permissions.go` |
| 11 | Approving leave marks those working days `LEAVE`; check-in on a leave day is refused | `leave/service.go` |
| 12 | Days off (per shift, roster or special schedule) and holidays are never absent; nobody is absent before they joined | `report/service.go`, `attendance/plan.go` |
| 13 | Late only after start + grace; then counted **from scheduled start** (09:15 with 10-min grace = 15 min late). Set grace to 0 for strict | `attendance/plan.go` |
| 14 | Working minutes = check-out − check-in; early-leave minutes vs scheduled end (the next day for a cross-day shift) | `attendance/plan.go` |
| — | Special schedule > weekly roster > usual shift; one person's special schedules never overlap | `attendance/plan.go`, `schedule/service.go` |
| — | A night shift's events belong to the day it starts | `attendance/service.go` (`shiftFor`) |
| 15 | Every admin change is audited; `audit_logs` has DB triggers that block UPDATE/DELETE | migration `0001` |
| 19 | Exports use the same scope as reports and are audited | `httpapi/handlers.go` |
| 20 | Employees are deactivated, never deleted; FKs keep history | `employee/service.go` |

Times are stored in UTC; `work_date` is the calendar day in the company timezone
(`Asia/Phnom_Penh` by default).

### Scheduled jobs (settings, no restart needed)

| Setting | Default | Job |
|---|---|---|
| `missing_checkin_alert_time` | 10:00 | Tells each branch admin (or branch review chat) who in their branch hasn't checked in, and the company admins everyone |
| `daily_report_time` | 18:00 | Posts the daily report (+ "need attention") to the company group, and each branch's report to its own group |
| `day_close_time` | 23:50 | Writes `ABSENT` rows for scheduled staff with no check-in |
| — | 08:00 | If the last database backup failed or is over a day old, tells the company admins |

A Redis lock runs each job once per day across replicas; a run missed by a restart is
caught up within 2 hours.

## REST API

Errors look like `{"error":{"code":"ALREADY_CHECKED_IN","message":"…"}}`. Dates accept
`DD-MM-YYYY` or `YYYY-MM-DD`. List endpoints always return arrays (`[]`, never `null`).

### Admin panel — `/api/v1` (session cookie or `Bearer` token)

| Method & path | Permission | Notes |
|---|---|---|
| `POST /auth/login` · `GET /auth/me` · `POST /auth/logout` | — | login: 10/min/IP; `me` includes company name + timezone |
| `GET /employees?q=&branch_id=&department_id=&status=&include_inactive=` · `GET /employees/:id` | scoped | `has_login` = has a panel login |
| `POST /employees` · `PUT /employees/:id` · `DELETE /employees/:id` | staff.manage | delete = deactivate |
| `POST /employees/:id/user` | super admin | panel login |
| `GET /branches` · `GET /branches/:id` | scoped | company-wide users see all; others their own |
| `POST /branches` · `PUT /branches/:id/admin {employee_id\|null}` | owner | assign / remove the branch admin |
| `PUT /branches/:id` | owner, or that branch's admin | branch admins: office location + Telegram chats only |
| `GET /departments` · `GET /work-schedules` | any | shifts, with how many staff use each |
| `POST /work-schedules` · `PUT /work-schedules/:id` · `DELETE /work-schedules/:id` | schedules.manage (owner, HR) | `work_type` `NORMAL`\|`CROSS_DAY`; a shift in use or the default can't be deleted |
| `GET /roster?from=&to=&branch_id=&department_id=&q=` | scoped | matrix: each person × day with the effective shift and its `source` (`DEFAULT`/`ROSTER`/`SPECIAL`), holidays, leave; ≤ 31 days |
| `PUT /roster {items:[{employee_id,date,work_schedule_id\|off\|reset}]}` · `POST /roster/copy {from,to}` | staff.manage, scoped | ≤ 500 changes; copy repeats one week's entries onto another |
| `GET /roster/export?format=xlsx\|csv&from=&to=…` | report.export | colour-coded sheet + shift list; audited |
| `GET /schedule-overrides?when=active\|upcoming\|past&branch_id=&q=` | scoped | special schedules |
| `POST /schedule-overrides` · `PUT /schedule-overrides/:id` · `DELETE /schedule-overrides/:id` | staff.manage, scoped | `kind` `SHIFT`\|`CUSTOM`\|`OFF`, `work_days`, `reason`; overlaps → 409 |
| `GET /attendance?employee_id=&branch_id=&department_id=&from=&to=&status=&confirmation=&page=` | scoped | |
| `GET /attendance/:id` · `GET /attendance/photos/:id` | scoped | photo streamed by the API |
| `POST /attendance` · `PUT /attendance/:id` | attendance.edit | manual create/edit, `reason` required |
| `GET /attendance/pending` | reviewer | |
| `POST /attendance/confirmations/:id/confirm` · `/reject {reason}` | reviewer | |
| `GET /leave?status=&employee_id=&branch_id=&from=&to=` · `GET /leave/:id` · `GET /leave/types` · `GET /leave/pending` | scoped | |
| `POST /leave/:id/approve` · `/reject {note}` | reviewer | |
| `GET /reports/daily?date=&branch_id=` | scoped | counts + rows |
| `GET /reports/summary?from=&to=&employee_id=&branch_id=&department_id=&status=` | scoped | rows + per-staff summaries + totals |
| `GET /reports/export?format=xlsx\|csv&from=&to=&employee_id=&branch_id=&department_id=` | report.export | includes Branch and Shift columns |
| `GET /settings` · `PUT /settings {key: value}` | settings.view / owner | `{settings, backup}`; `telegram_bot_token` is write-only and verified with Telegram |
| `GET /holidays?year=` · `POST /holidays` · `DELETE /holidays/:id` | super admin to change | |
| `GET /audit-logs?action=&employee_id=&before_id=` | super admin | |
| `GET /health` | — | `{"status":"ok","database":"connected","redis":"connected","storage":"connected"}` |

### Mini App — `/api/v1/app` (`Authorization: tma <initData>`)

| Method & path | Who | Notes |
|---|---|---|
| `GET /me` | any employee | identity, profile, company settings |
| `GET /attendance/today` · `GET /attendance/history?month=YYYY-MM` | self | |
| `POST /attendance/check-in` · `/check-out` | self | multipart: `photo`, optional `latitude` `longitude` `accuracy` |
| `GET /leave/types` · `GET /leave/mine` · `POST /leave/preview` · `POST /leave` · `POST /leave/:id/cancel` | self | |
| `GET /attendance/pending` · confirm/reject · `GET /leave/pending` · approve/reject · `GET /reports/daily` | reviewer | same handlers as the panel |
| `GET /attendance/:id` · `GET /attendance/photos/:id` · `GET /leave/:id` | scoped | |

## Security checklist

- Mini App: `initData` HMAC verified with the bot token (constant-time), max age
  `MINIAPP_SESSION_TTL` (24h); identity = numeric Telegram user ID (usernames are
  informational only). 300 req/min/user, 20 check-in/out uploads/min/user.
- Bot token: stored AES-256-GCM-encrypted (key derived from `JWT_SECRET`), so a leaked
  database backup doesn't leak it; never returned by the API or written to the audit
  log. Changing `JWT_SECRET` means re-entering the token in Settings.
- Branch scope is enforced in the API (`auth/permissions.go`): branch admins can't read,
  edit or review other branches or company-wide admins, and a demoted admin's session
  ends immediately.
- Admin panel: bcrypt (cost 12) passwords; HS256 JWT in an HttpOnly, SameSite=Strict
  cookie; `X-Requested-With` required on cookie writes; account re-checked on every
  request; login 10/min/IP (API) + nginx.
- Served with CSPs: the panel can't be framed (`frame-ancestors 'none'`); the Mini App
  can only be framed by Telegram Web.
- GORM parameterised queries everywhere; request bodies capped at 1 MB (8 MB for photo
  uploads); input validated.
- Photos: content sniffed (JPEG/PNG/WebP only), private bucket, streamed after a scope check.
- CSV/Excel cells starting with `= + - @` are neutralised (formula injection).
- Secrets only from environment; containers run as non-root.

## Tests

```bash
cd backend && go test ./...                 # unit tests
TEST_DATABASE_URL='root:pass@tcp(127.0.0.1:3306)/' TEST_REDIS_URL='redis://127.0.0.1:6379/15' \
  go test ./internal/httpapi -run Integration -v   # full flow against MySQL + Redis
TEST_DATABASE_URL='root:pass@tcp(127.0.0.1:3306)/' go test ./internal/demo -v   # demo data is consistent
cd miniapp && npm run typecheck && npm run build
cd admin && npm run typecheck && npm run build
```

The integration test creates and drops its own database and drives the real Mini App and
admin panel APIs: initData validation, unregistered users, check-in/out with photo
uploads and reviewer notifications, manager scope, double check-in/out, photo reuse,
reject → re-submit, leave overlap/approval/leave-day blocking, day close (absent vs
weekend), the daily report, panel login + session cookie + CSRF header, manual edit +
audit, audit immutability, photo streaming, Excel export and deactivation.

**First build:** run `go mod tidy` once in `backend/` and commit the resulting `go.sum`
(the Dockerfile also runs it, verifying checksums against sum.golang.org).

### Backups

`backup` runs `mysqldump --single-transaction` daily at `BACKUP_TIME` into `./backups`
and keeps `BACKUP_RETENTION_DAYS`. Each run writes `backups/.last_backup.json`; the API
(which mounts `./backups` read-only) shows the last run in *Settings* and alerts the
company admins in Telegram if a backup failed or none ran for over a day.
Run one now: `docker compose exec backup backup.sh now`.
Restore: `gunzip -c backups/attendance-YYYYMMDD-HHMM.sql.gz | docker compose exec -T database mysql -uroot -p"$MYSQL_ROOT_PASSWORD" attendance`.
Back up the photo bucket too (or rely on R2/S3 durability).

## Migrating from the chat-bot version

- Remove the old webhook so Telegram stops delivering messages to a URL that no longer
  exists: `curl -s https://api.telegram.org/bot<your-bot-token>/deleteWebhook`.
- Telegram moved from `.env` to *Settings → Telegram* in the admin panel. These are no
  longer used — delete them from `.env`: `BOT_TOKEN`, `MINIAPP_URL`, `TELEGRAM_GROUP_ID`,
  `TELEGRAM_ADMIN_CHAT_ID` (now *Review chat ID*), `BACKUP_NOTIFY_CHAT_ID`, `CORS_ORIGINS`,
  `BOT_MODE`, `BOT_WEBHOOK_URL`, `TELEGRAM_WEBHOOK_SECRET`, `INTERNAL_API_TOKEN`.
- Migration `0003` adds branches and puts every existing non-admin employee into
  *Main branch*.
- Confirm/Reject buttons on notifications sent before the upgrade no longer do anything;
  review those items in the admin panel or the Mini App's Team tab.
- Existing employees, Telegram links, attendance, photos and leave are unchanged.
