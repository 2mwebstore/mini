# Attendance & Leave Management (Telegram Mini App + HR admin panel)

Employees check in and out (with GPS where their branch asks for it — no photo), request
leave (full or half days) and see their own records in a **Telegram Mini App**. The
owner, HR, branch admins and managers confirm attendance, approve leave, manage staff
and run reports in a web **admin panel**. The
company can have **several branches**, and each branch runs on its own: its own
Telegram bot, GPS rules and office, departments, positions, shifts, leave types and
holidays. A branch is run by a branch admin; a client's **Company Admin** sees only the
branches they were given or created themselves. A Go API backs both apps and sends
Telegram notifications. Telegram (each branch's bot token, Mini App URL, group chats) is
configured on the branch page of the admin panel — not in `.env`.

```
attendance-system/
├── backend/                 Go API (Gin, GORM, MySQL) + scheduler
│   ├── cmd/api/             serve | migrate | create-admin | seed-demo | clear-data
│   ├── internal/
│   │   ├── auth/            JWT sessions, Mini App initData, RBAC (permissions.go)
│   │   ├── employee/        staff (employees) and their Telegram link
│   │   ├── branch/          branches: details, admin, GPS rules + office, Telegram
│   │   ├── bots/            each branch's Telegram bot (token decrypted, cached)
│   │   ├── attendance/      check-in/out, late/early/hours, review, manual edits
│   │   ├── leave/           requests, approval → marks days as LEAVE
│   │   ├── report/          daily/period/staff reports, Excel/CSV
│   │   ├── scheduler/       missing check-in alert, daily report, day close
│   │   ├── notification/    Telegram notifications (+ notifications table)
│   │   ├── telegram/        send-only Bot API client
│   │   ├── storage/         S3-compatible photo storage (R2 / S3 / MinIO)
│   │   ├── settings/        company settings (name, timezone, jobs), cached
│   │   ├── secret/          encryption of stored credentials (bot tokens)
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
  API verifies it with the branch bots' tokens and maps the numeric Telegram user ID to
  an employee. In the admin panel, people sign in with email + password.
- **One bot per branch, no chat bot.** Each branch's bot from @BotFather hosts the Mini
  App (its menu button opens it) and sends that branch's notifications. Messages sent
  *to* a bot are not read.
- **Notifications** about a check-in or leave request go through the employee's branch
  bot, with the time, lateness, **the employee's reason** (when late or leaving early),
  the location and a **Review in app** button: to the branch's review chat if it has
  one; otherwise by DM to the employee's manager and branch admin — or, when neither is
  reachable, to everyone who reviews that branch (HR, the owner, admins assigned to it).
  Employees are told when their attendance or leave is reviewed. The daily report goes
  to each branch's own group.

## Quick start

```bash
cp .env.example .env            # fill in secrets: openssl rand -hex 32
docker compose up -d --build
docker compose logs -f api

# Create the owner — a system user (password from env, not a flag):
docker compose exec -e ADMIN_PASSWORD='a-long-password' api \
  /app/api create-admin -name "Owner Name" -email owner@company.com
```

Then open `https://<your-domain>/admin/`, sign in, and:

1. **Branches:** rename *Main branch* (all existing staff start there) and add the
   others. *Add branch* can start a branch with a default shift and the usual leave
   types (ticked by default); untick it to set everything up yourself.
2. **Each branch's page → Telegram:** paste that branch's bot token from @BotFather and
   the Mini App URL (`https://<your-domain>/app/`). The token is checked with Telegram,
   stored encrypted, and the bot's **Open App** menu button is pointed at the Mini App.
   Until a branch has a bot its staff can't open the Mini App and it gets no
   notifications. Optionally add the branch's group chat ID (daily report) and a review
   chat. On the same page, **Location (GPS)** sets whether check-ins ask for the
   location, require it, or refuse it outside the office radius.
3. **Each branch's setup lists** — departments, positions, shifts, leave types and
   holidays. Every one belongs to a single branch (pick the branch at the top of each
   list); nothing is shared, so two branches can have different hours and allowances.
4. **Employees → Add employee** for each member of staff, choosing their branch (its own
   departments, positions and shifts are then offered). Everyone needs their numeric
   **Telegram user ID** for the Mini App: they open their branch's bot, tap **Open
   App**, and the "No access" screen shows their ID.
5. **Administration → System users → New system user** for each admin account (HR, a
   Branch Admin per branch…). Choosing *Branch Admin* and a branch makes them that
   branch's admin.

**Setting up a client.** Give the client one system user with the **Company Admin** role
(no branch needed), and either assign them a branch on *Administration → Branch
assignment* or let them create their own: a branch they create is theirs at once. They
see and manage only those branches — staff, attendance, leave, schedules, setup lists,
reports, Telegram and GPS — and set up the whole company themselves. They cannot see
other branches, manage system users or change company settings.

**Employees and system users are separate.** *Employees* are staff: they check in and
out and request leave in the Mini App, and never sign in to the panel. *System users* are
admin panel accounts: they don't check in, don't take leave and aren't in staff lists,
reports or schedules. Someone who does both (a branch manager who also runs the branch in
the panel) has one of each — an employee record with their Telegram account for
checking in, and a system user for the panel.

Roles: system users have admin roles — `super_admin` (the owner), `hr_admin`
(company-wide HR), `branch_admin` (one branch) or a custom admin role; employees have
`staff`, `manager` or a custom role of that kind. The owner (and a Company Admin, for
their own branches) creates branches; the owner manages every system user, and a
branch's admins the accounts and roles of their branches. A manager reviews the people whose
**Managers** include them, in the Mini App. Someone can have **several managers**
(*Managers* in the employee form — people of their branch whose role reviews a team):
each of them reviews the person and gets their review messages.

### HTTPS

Telegram only opens Mini Apps over HTTPS. Pick one:

1. **Host Nginx + Certbot**: set `NGINX_HTTP_PORT=8088` in `.env` and proxy your host
   server block to `http://127.0.0.1:8088`.
2. **Certificates in the stack:** put `fullchain.pem` / `privkey.pem` in
   `ops/nginx/certs/`, rename `ops/nginx/conf.d/ssl.conf.example` to `ssl.conf`.
3. **Cloudflare proxy / tunnel** in front of port 80.

### Photo storage

The Mini App no longer takes photos; the bucket keeps the photos of records made while it
did, and still accepts one if a client sends it. Any S3-compatible bucket works. For
Cloudflare R2:

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

   Never change `JWT_SECRET` afterwards: it signs sessions and encrypts the branch bot
   tokens (a new secret means everyone signs in again and every branch's token must be
   re-entered).
4. **Settings → Networking → Generate Domain**, target port `8080`. Only this one service
   gets a domain: do not add separate services for `admin/`, `miniapp/` or `backend/`
   (each would be on its own domain, where the panel and the Mini App cannot reach the API). When the deploy is
   green, `https://<domain>/health` shows database, redis and storage `connected`.
5. Create the owner from a terminal with the [Railway CLI](https://docs.railway.com/guides/cli)
   (`railway link` to this project and the app service first):

   ```bash
   railway ssh
   ADMIN_PASSWORD='a-long-password' /app/api create-admin -name "Owner Name" -email you@company.com
   ```

6. Open `https://<domain>/admin/` and continue from step 1 after *Quick start* above
   (each branch's *Telegram*, Mini App URL `https://<domain>/app/`).

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

`miniapp/` opens inside Telegram from the bot's menu button. The home screen has a
**Check in** and a **Check out** tile side by side, each with its own time, on-time /
late (or left-early) status, review state and button; checking in or out takes one tap
(plus the location when the branch asks for it — there is no photo). Employees also see
their monthly history, request and cancel leave — full days or a **half day** — and view
their profile. Managers and admins also get a **Team** tab to confirm or reject
attendance (each card shows where the person was), approve leave and see today's team
report — handy on the go; the admin panel has the full tools.

**Setup.** Each branch has its own bot: set its token and the Mini App URL on the
branch's page (*Branches → a branch → Telegram*); the API points that bot's chat menu
button at the Mini App (on save and on every start). Staff open the Mini App from their
own branch's bot. Optional: in @BotFather → *Bot Settings →
Configure Mini App*, set the same URL — this enables the bot profile's **Open** button and
the `t.me/<bot>?startapp=team` links used in group-chat notifications. Employees see
their branch on the home screen and profile; a branch admin's **Team** tab covers their
branch.

**Authentication.** Every request sends Telegram's signed `initData` as
`Authorization: tma <initData>`. The API verifies the HMAC against the branch bots'
tokens (`auth/webapp.go`, `bots/`) and rejects launches older than `MINIAPP_SESSION_TTL` (24h; the user
just reopens the app). `/api/v1/app` exposes only what the app needs; reviewer routes
reuse the admin panel's handlers and permission checks. Panel sessions are not accepted
there, and `initData` is not accepted on the panel's routes.

**Leave form.** The leave type is a searchable picker (a bottom sheet listing each type
with its colour and days left). *Full day* takes a date range; *Half day* takes one date
and the half, named and timed from that person's shift on that date: a day shift
(09:00–17:30) splits into **Morning** 09:00–13:15 and **Afternoon** 13:15–17:30; a
cross-day night shift (22:00–06:00) into **First half** 22:00–02:00 and **Second half**
02:00–06:00 (next day). The form shows the shift and the balance left after the request.

**No photos.** Check-ins have no camera or upload. Photos from earlier records are kept
and still shown in the admin panel.

**Reasons.** Checking in late (after the grace period) or checking out before the
shift ends asks **why** — tap a common reason (Traffic, Feeling unwell, Approved by my
manager…) or write one (up to 255 characters). The button stays off until a reason is
given, and the API refuses a late check-in or early check-out without one
(`REASON_REQUIRED`). The reason is shown on the home tiles, in the history, on the
reviewer's card and in their Telegram message, in the admin panel's Reason column and in
report exports.

**Location** uses Telegram's LocationManager (Bot API 8.0+) and falls back to browser
geolocation, following the GPS rules of the person's branch (*Branches → a branch →
Location (GPS)*).

## Overtime

Employees ask for **overtime** in the Mini App (*Home → Overtime → New request*): a date
and a **From–To** range — by default **from the end of that day's shift to an hour
later** (on a day off, from the end of their usual shift); a "To" at or before "From"
ends the next day — and a **reason** (common reasons are one tap). It must be outside
the shift, 15 minutes to 12 hours, not overlapping their other overtime, and at most 30
days back / 60 ahead.

Their **line manager** (each of their managers), or whoever reviews their branch,
**approves or rejects** it — on the Mini App's *Team* tab or the admin panel's
*Attendance → Overtime* (filters, 10 a page, approve / reject icons), and both sides get
a Telegram message. The employee can **cancel** a request while it is pending, or once
approved until it starts.

**Reports** count overtime by the date it is for: per person *OT approved* and *OT
pending* (and totals) on the Reports page, and in the Excel/CSV export (daily sheet:
*Overtime Approved / Pending (min)*; summary sheet: *Overtime Approved / Pending*). The
dashboard's "waiting for review" includes overtime.

## Excel report

*Reports → Export Excel* downloads a report laid out like a CRM's:

- **Summary** (opens first) — a title block (company, period, filters, who made it and
  when), **KPI cards** (staff, working / present / late / absent / leave days, hours
  worked, overtime approved and pending) and one row per person with a **totals** row:
  days, late minutes, waiting-review and rejected days, hours worked, average per day,
  overtime approved and pending (as h:mm and as decimal hours, for payroll).
- **Daily attendance** — every person-day: shift, check-in/out, worked, late / early
  minutes, a **coloured status** (present green, late / early amber, absent red, leave
  blue), leave type, review and reviewer, the check-in/out reasons and the day's
  overtime.
- **Overtime** — each approved and pending request (date, from–to, length, reason,
  status, reviewer) with APPROVED / PENDING / TOTAL lines.

Every sheet is an Excel table (filter buttons, banded rows) with frozen headers and
names, and prints landscape one page wide. The CSV export stays one plain row per
person-day.

## Branches

Every staff member, manager and branch admin belongs to one branch; the owner and HR
admins work across all branches. **Everything is per branch**: each has its own Telegram
bot and chats, GPS rules and office, and its own departments, positions, shifts (with a
default one), leave types and holidays. Pick the branch at the top of each setup list;
the employee form, the roster and special schedules offer only the person's own
branch's items. Each branch has **at most one branch admin**, and a
person can admin only one branch. Only the owner assigns them (*Branches → a branch →
Assign a new admin*): the person gets the Branch Admin role and moves to that branch;
a replaced admin becomes staff, and their panel session ends at once. Deactivating a
branch admin leaves the branch without one.

A branch admin has HR's tools limited to their branch: they see and review only their
branch's people, add staff (always into their branch, as staff or manager), correct
attendance, approve leave, manage their branch's shifts, holidays and leave types, and
export their branch's reports. On **My branch** they set the branch's **Telegram** (its
bot, Mini App URL, a group for its daily report and an optional review chat) and its
**Location (GPS)**. They cannot rename or close the branch, change its admin, or see
company settings.

**Company Admin (a client).** The `company_admin` role is for a client who sets up their
own company: give them the role (no branch needed) and either assign branches on *Branch
assignment* or let them create their own on *Branches* — a branch they create is added
to their access at once. They see only those branches and manage everything in them
(staff, attendance, leave, schedules, setup lists, reports, GPS, Telegram); they cannot
see other branches or change company settings.

They also run **Administration** for their branches (*Admin accounts & roles of their
branches*, `users.branch` — Branch Admins have it too, for their own branch): on
*System users* they create, edit, disable, reset the password of and delete admin
accounts that work only in their branches — Branch Admins (which become that branch's
admin), more Company Admins, or a role of their branch — never the owner, HR or any
company-wide role, and never a role with more powers than their own. Every account they
make is placed in one of their branches; on *Branch assignment* they can add only their
own branches. Accounts outside their branches don't exist for them.

**Branch roles.** On *Role management* they see and edit only their branches' own roles
(the owner sees every role, each labelled *Company* or with its branch). A branch role is
made for one branch, may hold only permissions its maker has (plus checking in and
leave, for staff roles), and is offered only for that branch's people — in the employee
form and on System users (`ROLE_OTHER_BRANCH` otherwise). Company roles stay the owner's.
The demo's client has one: *Shift Supervisor* (Kampot Coffee Co.).

**GPS per branch.** On each branch's page: ask for the location at check-in/out, require
it, and/or refuse check-ins outside the office radius (which needs the office point).
New branches start with GPS off. Paste a Google Maps / OpenStreetMap link or "lat, lng",
or use the browser's current location. Without an office point the position is recorded
but not checked.

**Branch access.** *Administration → Branch assignment* limits system users to specific
branches. A user with a company-wide role (*View all branches*) and **no branches ticked
manages all branches**; ticking some limits them to those. A branch admin always has
their own branch, and extra ticks add more. Team-only roles (managers) are not affected.
Branch access limits staff lists, attendance, leave, balances, reports and the roster;
what the user may do there comes from their role. Nobody can limit their own access.

## HR admin panel

`admin/` is served at `/admin/` (the site root redirects there). The menu is grouped:
**Attendance** (Records, Reports), **Leave** (Requests, Balances, Types), **Schedules**
(Weekly roster, Special schedules, Shifts, Holidays), **Employees** (Employees,
Departments, Positions), **Branches**, **Administration** (System users, Branch
assignment, Role management) and **Settings** (General, Audit log). A branch admin also
gets **My branch**.
Pages a role cannot use are hidden. What the built-in roles may do (change it on
*Role management*):

| Page | Owner (super admin) | HR admin | Company Admin (a client) | Branch admin |
|---|---|---|---|---|
| Dashboard — today's counts, each person's shift, who needs attention | all (branch filter) | all (branch filter) | their branches | their branch |
| Attendance › Records — check-in and check-out in their own columns (time, on time / late / early, inside/outside pin) and a **Reason** column (why late / why early, in the employee's words); the review status is not in the list: **View** opens the full record (schedule, each side's status, reason, location, reviewer and review history; confirm/reject there); **Edit** and **Delete** (reason required, audited) on each row | ✓ | ✓ | their branches | their branch |
| ↳ add a record / edit times (reason required, audited) | ✓ | ✓ | their branches | their branch |
| Leave › Requests — all statuses by default; full / half day filter (half days show which half and its hours); approve/reject | ✓ | ✓ | their branches | their branch |
| Leave › Balances — entitlement, carried over, used, pending, left per type and year (one branch at a time) | edit | edit | edit | edit their branch |
| Leave › Types — per branch: allowance per year, paid, manual/automatic approval, limits | edit | edit | their branches | their branch |
| Schedules › Weekly roster — shift per person per day (from their branch's shifts), copy last week, Excel/CSV export | edit | edit | their branches | their branch |
| Schedules › Special schedules — date-range overrides (another shift of the people's branch, custom hours, days off) for one or **several people at once** | ✓ | ✓ | their branches | their branch |
| Schedules › Shifts — per branch: hours, grace, work days, normal or cross-day (night); each branch has its own default | edit | edit | their branches | their branch |
| Schedules › Holidays — per branch; one holiday can be added to several branches at once | edit | edit | their branches | their branch |
| Employees — add, edit, link Telegram, set branch/department/position/manager/role, deactivate; filter by branch, department and position | ✓ | ✓ (not admins) | their branches | their branch |
| Employees › Departments, Positions — per branch: add, rename, delete unused | ✓ | ✓ | their branches | their branch |
| Administration › System users — admin accounts (separate from employees): role, branch, Telegram, status, reset password, delete | ✓ | — | their branches' accounts | their branch's accounts |
| Administration › Branch assignment | ✓ | — | their branches' accounts | their branch's accounts |
| Administration › Role management — company roles and each branch's own roles | ✓ (all) | — | their branches' roles | their branch's roles |
| Branches — add/edit/close branches; each branch's page has its details, admin, **Location (GPS)** and **Telegram** (own bot) | ✓ (+ assign admins) | — | add, and edit their own | My branch: GPS + Telegram |
| Reports — period summary per employee; **Excel report** (CRM-style: Summary, Daily attendance, Overtime sheets) and CSV | ✓ | ✓ | their branches | their branch |
| Settings › General — company name, timezone, confirmations, scheduled jobs | edit | view | — | — |
| Audit log | ✓ | — | — | — |

Managers are employees: they review their team's attendance and leave in the Mini App
(*Team* tab), not in the panel.

**Map pins** next to check-in and check-out times — on the dashboard, the attendance
list and a record's View — open the spot in **Google Maps** (green: inside the office
radius, amber: outside, grey: the branch has no office point).

**Every list shows 10 rows per page**, with *Rows* (10 / 25 / 50 / 100) and page buttons
under it. Row actions are icon buttons with a coloured background — **View** (blue),
**Edit** (indigo), **Delete** (red), plus *Reset password* (amber) and *Approve* /
*Reject* on leave — each naming what it does in its tooltip.

**Roles.** A role is a set of permissions (*Administration → Role management*). The five
built-in roles can be edited but not deleted; the owner (Super Admin) always has every
permission, so nobody can be locked out. New roles can start from an existing one's
permissions. Anyone whose role has a permission beyond their own attendance and leave can
sign in to the panel; *Grant admin roles* is needed to give someone a role with admin
powers. You cannot remove *Manage system users* from your own role, change your own role,
or demote the last owner. Role changes apply within 30 seconds on every replica.

**System users** (*Administration → System users*) are admin panel accounts, separate
from employees: name, email (sign-in), password, phone, an admin role, a branch for
branch-level roles (a *Branch Admin* takes that branch's admin slot — one per branch) and
an optional Telegram user ID for review notifications and the Mini App's Team tab (a
system user who opens the Mini App sees only *Team* and *Profile*). Edit, disable, reset
the password or delete an account; deleting keeps its past actions in the audit log.
Managers and staff are employees, not system users.

**Branch assignment** gives each system user a multi-select of branches: none chosen =
every branch (company-wide roles); a branch admin always has their own branch, and any
chosen add to it.

**Deleting** anything in the panel (an attendance record, a special schedule, a shift, a
leave type, a role, a department or position, a system user) asks first in one
confirmation dialog that names what is removed. Deleting an attendance record also asks
for a reason: the record, the reason and who did it go to the audit log, and its reviews,
locations and photos are removed. Every select box has a search field.

**Leave policy.** Each leave type has a yearly allowance (0 = not counted), paid or
unpaid, **manual** or **automatic** approval, an optional maximum per request and minimum
notice, and whether people may go over the allowance. A person's balance for a year is
their entitlement (the type's allowance unless HR set their own) + days carried over −
approved days; pending requests' days are reserved. Days count in the year a request
starts. A request over the balance, the per-request maximum or inside the notice period
is refused with a clear message; automatic types are approved on submit.

**Half days.** A request is a full day (or range) or one half of a single working day
(0.5 days off the balance). The half is cut from that day's shift at its midpoint, so it
follows the person's work type: 09:00–17:30 gives 09:00–13:15 / 13:15–17:30, a night
shift 22:00–06:00 gives 22:00–02:00 / 02:00–06:00 the next morning. The hours are saved
on the request. Both halves of a date may be taken, but not the same half twice or a
half next to a full day. An approved half day does not mark the day as leave: the person
checks in for the other half, and late / early-leave minutes are measured against it
(a morning off with a 09:00–17:30 shift means starting at 13:15). Approving one after the
day was worked re-measures that day. Reports count half days as 0.5 leave days.

**Departments and positions** are managed lists. Renaming a position renames it on every
employee; a department or position still in use can't be deleted.

**Special schedules** can be given to several people at once: tick them in the
*Employees* picker (search a department or branch name, then *Tick all matches*). Each
person gets their own row, so it can be edited or deleted for one person later. If
anyone already has a special schedule on those dates, nothing is saved and the message
names them.

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
ADMIN_PASSWORD='a-long-password' go run ./cmd/api create-admin -name "Owner Name" -email you@company.com
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
tunnel`) and set that as the Mini App URL of a test bot on a branch's page (*Telegram*).
Both apps need a current browser / web view (Chrome 111+, iOS 16.4+).

### Demo data

To explore the admin panel without real staff, fill a development database with sample
data (from `backend/`):

```bash
go run ./cmd/api seed-demo
```

It adds four branches — Main branch, Siem Reap and Battambang, plus **Kampot Coffee Co.**,
a client's café set up by its own *Company Admin* — each with its own departments,
positions, shifts (office branches: Office Hours, a morning and a night (cross-day)
shift; the café: Café Hours), leave types and Cambodia's fixed-date public holidays.
19 employees (staff and managers, plus a branch manager per branch) with four weeks of
attendance up to today — on time, late, early leave, absent and missing check-out days,
with **reasons** for the late and early ones, GPS points and reviews (some still pending,
one rejected). System users: the owner, an HR admin, a Branch Admin per branch (the
branch managers' admin accounts — separate records from their employee ones), a
*Regional Supervisor* (a custom role) limited to Siem Reap and Battambang, and the
client's **Company Admin** (`client@demo.local`), who sees only Kampot Coffee Co. Also
approved, pending, rejected and cancelled leave requests (two of them half days: a night
shift's first half and a day shift's afternoon), a few weekly roster days, three special
schedules and leave allowances (one person with a custom entitlement and carried-over
days). The command prints the system users' logins: all of them use the password
`Demo@12345` (set `DEMO_PASSWORD` to choose another). If the database already has an
owner, that owner is kept (with their own password) and no demo owner is created.

Demo employees have codes starting with `D-` and placeholder Telegram IDs
(9900000001…), so they cannot open the Mini App; set a bot on their branch and link your
own Telegram ID to one of them in the admin panel to try it. Running the command again
does nothing. It refuses to run with `APP_ENV=production` — never seed a live database.

### Clearing data

To start over — before a client demo, or before entering real data — wipe the business
data (from `backend/`):

```bash
go run ./cmd/api clear-data -yes     # without -yes it only says what it would delete
go run ./cmd/api seed-demo           # optional: fresh demo data
```

It deletes every branch with its Telegram, GPS and setup lists, all employees, system
users other than the owner(s), attendance, leave, balances, schedules, holidays and
notifications. It keeps the database schema, roles and permissions, company settings,
the owner account(s) and the audit log (with a `data.clear` entry). Photo files already in
storage are not removed. It refuses `APP_ENV=production` unless given `-force`; never run
it on a live database.

## Business rules (as implemented)

| # | Rule | Where |
|---|---|---|
| 1–3 | One check-in and one check-out per day; no check-out without check-in | `attendance/service.go` (row lock + unique `(employee_id, work_date)`) |
| — | A **rejected** check-in/out can be re-submitted; history is kept (old review marked `SUPERSEDED`); each submission keeps its own location | same (migration `0006`) |
| 4–5 | No photo needed (the Mini App doesn't take one). A photo a client still sends is checked and can't be a re-used image | `attendance/service.go` (SHA-256, migration `0002`) |
| 6 | GPS per branch: `gps_enabled`, `gps_required`, `gps_reject_outside`, office lat/lng/radius (NULL = off; refusing outside needs the office) | `branches`, `attendance.GPSRulesFor` |
| — | A late check-in (after the grace period) or an early check-out needs a reason (≤ 255 characters), stored as `check_in_reason` / `check_out_reason` | `attendance/service.go` (`REASON_REQUIRED`, migration `0008`) |
| — | Departments, positions, shifts, leave types and holidays belong to one branch; a person only gets their own branch's (a move to another branch drops the old ones and gives the new branch's default shift) | `employee/`, `schedule/`, `leave/` (migration `0008`) |
| 7 | `confirmation_required=false` auto-confirms | `company_settings` |
| 8–10 | Staff: self · Manager: the people they manage (`employee_managers`; a person may have several managers) · Branch admin: their branch (+ assigned) · company-wide roles: all, or only their assigned branches. No self-review except super admin | `auth/permissions.go`, `auth/roles.go` |
| 11 | Approving leave marks those working days `LEAVE`; check-in on a leave day is refused. A **half day** marks nothing: the other half is worked, late/early measured against it | `leave/service.go`, `attendance/plan.go` (`Halves`, `WithHalfOff`) |
| — | A half day is one working date; its hours are the first or second half of that day's shift (normal or cross-day). Two different halves of a date may both be taken | `leave/service.go` |
| — | Leave can't exceed the balance (unless the type allows it), the per-request maximum or the notice period | `leave/policy.go` |
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
| `missing_checkin_alert_time` | 10:00 | Tells each branch admin (or branch review chat) who in their branch hasn't checked in, and the company admins everyone — through the branches' bots |
| `daily_report_time` | 18:00 | Posts each branch's daily report (+ "need attention") to that branch's group, through its bot |
| `day_close_time` | 23:50 | Writes `ABSENT` rows for scheduled staff with no check-in |
| — | 08:00 | If the last database backup failed or is over a day old, tells the company admins |

A Redis lock runs each job once per day across replicas; a run missed by a restart is
caught up within 2 hours.

## REST API

Errors look like `{"error":{"code":"ALREADY_CHECKED_IN","message":"…"}}`. Dates accept
`DD-MM-YYYY` or `YYYY-MM-DD`. List endpoints always return arrays (`[]`, never `null`).

**Lists are paged:** `GET` list endpoints of the admin API return
`{items, total, page, page_size}`, **10 items per page by default**. Ask with `?page=`
(from 1) and `?page_size=` (at most 500); `page_size=0` returns everything (used by the
panel's pickers). The Mini App's routes (`/api/v1/app/…`) return everything unless
asked. Attendance records page at most 200 at a time; the audit log at most 500.

### Admin panel — `/api/v1` (session cookie or `Bearer` token)

| Method & path | Permission | Notes |
|---|---|---|
| `POST /auth/login` · `GET /auth/me` · `POST /auth/logout` | — | login: 10/min/IP; `me` includes company name + timezone |
| `GET /employees?q=&branch_id=&department_id=&position_id=&status=&include_inactive=` · `GET /employees/:id` | scoped | staff only (system users are not employees) |
| `POST /employees` · `PUT /employees/:id` · `DELETE /employees/:id` | staff.manage | delete = deactivate; `manager_ids: [..]` sets their managers (active employees; `[]` = none; `manager_id` still sets one); responses carry `manager_ids` and, for one employee, `managers` |
| `GET /branches` · `GET /branches/:id` | scoped | company-wide users see all; others the branches they reach; `telegram_bot_set` / `telegram_bot_username` (the token itself is never returned) |
| `POST /branches {code, name, address?, starter?}` | branches.manage (owner) or branches.create (Company Admin) | `starter: true` adds a default shift and the usual leave types; a creator limited to some branches gets the new one added to their access |
| `PUT /branches/:id/admin {employee_id\|null}` | owner, or users.branch in their branches | assign / remove the branch admin (users.branch: one of their own accounts) |
| `PUT /branches/:id` | owner, a creator for branches they reach, or that branch's admin | `gps: {enabled, required, reject_outside}`, office lat/lng/radius or `clear_office`, `telegram_bot_token` (write-only, checked with Telegram, `""` removes the bot), `miniapp_url`, `telegram_group_id`, `telegram_review_chat_id`; branch admins: only these, not code/name/address/status |
| `GET /departments?branch_id=` · `GET /positions?branch_id=` | scoped | `{id, branch_id, branch_name, name, employees}` |
| `POST\|PUT /departments[/:id]` · `POST\|PUT /positions[/:id] {name, branch_id}` · `DELETE …/:id` | staff.manage, in a branch they reach | `branch_id` on create; in use → 409 |
| `GET /roles` | any | company roles + the roles of the branches the caller reaches: permissions, head counts, `branch_id` / `branch_name`, `assignable` (may give it to a system user) and `editable` |
| `GET /roles/permissions` · `POST /roles` · `PUT /roles/:id` · `DELETE /roles/:id` | users.manage, or users.branch for their branches' roles | `{name, description, permissions[], branch_id?}`; the catalog lists what the caller may grant; a branch role's branch is fixed; built-in roles can't be deleted |
| `GET /system-users` · `POST /system-users {full_name, email, password, role, branch_id?, phone?, telegram_user_id?}` · `PUT /system-users/:id {…, status?}` · `DELETE /system-users/:id` | users.manage, or users.branch for their branches' accounts | admin accounts, separate from employees; admin roles only (a Branch Admin takes its branch); delete keeps the audit history. With users.branch: only accounts working in the actor's branches, only roles that are branch-level and within their own powers, `branch_id` required and theirs |
| `POST /system-users/:id/password {password}` · `PUT /system-users/:id/branches {branch_ids}` | users.manage, or users.branch | `branch_ids: []` = all branches (users.branch: only their own branches) |
| `GET /work-schedules?branch_id=` | scoped | each branch's shifts, with how many staff use each |
| `POST /work-schedules` · `PUT /work-schedules/:id` · `DELETE /work-schedules/:id` | schedules.manage, in a branch they reach | `branch_id` on create (a branch's first shift is its default); `work_type` `NORMAL`\|`CROSS_DAY`; a shift in use or the default can't be deleted |
| `GET /roster?from=&to=&branch_id=&department_id=&q=` | scoped | matrix: each person × day with the effective shift and its `source` (`DEFAULT`/`ROSTER`/`SPECIAL`), holidays, leave; ≤ 31 days |
| `PUT /roster {items:[{employee_id,date,work_schedule_id\|off\|reset}]}` · `POST /roster/copy {from,to}` | staff.manage, scoped | ≤ 500 changes; copy repeats one week's entries onto another |
| `GET /roster/export?format=xlsx\|csv&from=&to=…` | report.export | colour-coded sheet + shift list; audited |
| `GET /schedule-overrides?when=active\|upcoming\|past&branch_id=&q=` | scoped | special schedules |
| `POST /schedule-overrides` · `PUT /schedule-overrides/:id` · `DELETE /schedule-overrides/:id` | staff.manage, scoped | `kind` `SHIFT`\|`CUSTOM`\|`OFF`, `work_days`, `reason`; create takes `employee_ids: []` (≤ 300; or one `employee_id`) and returns `{items}` — all or nothing; overlaps → 409 naming who |
| `GET /attendance?employee_id=&branch_id=&department_id=&from=&to=&status=&confirmation=&page=` | scoped | |
| `GET /attendance/:id` · `GET /attendance/photos/:id` | scoped | `{attendance, photos, schedule, holiday, leave}`: the day's expected hours (less a half day), approved leave, each review with its reviewer, location and photo; photos (older records) streamed by the API |
| `POST /attendance` · `PUT /attendance/:id` · `DELETE /attendance/:id {reason}` | attendance.edit | manual create/edit/delete, `reason` required (audited); delete removes reviews, locations and photos |
| `GET /attendance/pending` | reviewer | |
| `POST /attendance/confirmations/:id/confirm` · `/reject {reason}` | reviewer | |
| `GET /leave?status=&day_type=FULL\|HALF&employee_id=&branch_id=&from=&to=` · `GET /leave/:id` · `GET /leave/types[?all=1&branch_id=]` · `GET /leave/pending` | scoped | `all=1` includes inactive types |
| `POST /leave/types` · `PUT /leave/types/:id` · `DELETE /leave/types/:id` | leave.manage, in a branch they reach | `branch_id` on create, `annual_days`, `is_paid`, `approval` `MANUAL`\|`AUTO`, `allow_exceed`, `max_days_per_request`, `min_notice_days`; a used type can only be deactivated |
| `GET /leave/balances?year=&branch_id=&department_id=&q=` | scoped | `{types, items:[{employee…, balances:[{allocated, carried, used, pending, remaining, available, custom}]}]}` |
| `PUT /leave/balances {employee_id, leave_type_id, year, allocated, carried, note}` · `DELETE /leave/balances?employee_id=&leave_type_id=&year=` | leave.manage | set / reset to the type's allowance |
| `POST /leave/:id/approve` · `/reject {note}` | reviewer | |
| `GET /reports/daily?date=&branch_id=` | scoped | counts + rows (each with `check_in_place` / `check_out_place`: lat/lng, distance, inside) |
| `GET /overtime?status=&employee_id=&branch_id=&from=&to=&page=&page_size=` · `GET /overtime/:id` · `GET /overtime/pending` | scoped / reviewer | overtime requests, newest date first |
| `POST /overtime/:id/approve` · `/reject {note}` | reviewer | the person's reviewers only; both sides get a Telegram message |
| `GET /reports/summary?from=&to=&employee_id=&branch_id=&department_id=&status=` | scoped | rows + per-staff summaries + totals |
| `GET /reports/export?format=xlsx\|csv&from=&to=&employee_id=&branch_id=&department_id=` | report.export | xlsx: Summary / Daily attendance / Overtime sheets (see *Excel report*); csv: the daily rows |
| `GET /settings` · `PUT /settings {key: value}` | settings.view / owner | `{settings, backup}`: company name, timezone, confirmations, job times (Telegram and GPS are per branch) |
| `GET /holidays?year=&branch_id=` · `POST /holidays {date, name, branch_ids}` · `DELETE /holidays/:id` | schedules.manage to change, in branches they reach | one holiday to several branches at once; a branch that already has one that day is skipped |
| `GET /audit-logs?action=&employee_id=&page=&page_size=` | super admin | newest first |
| `GET /health` | — | `{"status":"ok","database":"connected","redis":"connected","storage":"connected"}` |

### Mini App — `/api/v1/app` (`Authorization: tma <initData>`)

| Method & path | Who | Notes |
|---|---|---|
| `GET /me` | any employee | identity, profile, company settings |
| `GET /attendance/today` · `GET /attendance/history?month=YYYY-MM` | self | |
| `POST /attendance/check-in` · `/check-out` | self | form: optional `latitude` `longitude` `accuracy`, `reason` (required when late / leaving early → else `REASON_REQUIRED`), and an optional `photo` (no longer sent by the app) |
| `GET /attendance/today` | self | also `half_leave` on a half day off (the schedule shown is the half still worked); `check_in` / `check_out` say `late_minutes` / `early_minutes` and `reason_required` right now |
| `GET /leave/types` · `GET /leave/balances` · `GET /leave/mine` · `POST /leave/preview` · `POST /leave` · `POST /leave/:id/cancel` | self | body adds `day_type` `FULL`\|`FIRST_HALF`\|`SECOND_HALF`; preview returns `{days, balance, plan, problem}` — `plan` is the shift on the first date with its halves and work type, `problem` why it would be refused |
| `GET /attendance/pending` · confirm/reject · `GET /leave/pending` · approve/reject · `GET /reports/daily` | reviewer | same handlers as the panel |
| `GET /attendance/:id` · `GET /attendance/photos/:id` · `GET /leave/:id` | scoped | |
| `GET /overtime/plan?date=` | self | the shift that day and the suggested overtime: `default_from` (shift end), `default_to` (+1h) |
| `GET /overtime/mine` · `POST /overtime {date, start_time, end_time, reason}` · `POST /overtime/:id/cancel` | self | errors: `REASON_REQUIRED`, `OVERLAPS_SHIFT`, `OVERLAP`, `INVALID_LENGTH`, `TOO_FAR_BACK/AHEAD`, `NOT_CANCELLABLE` |
| `GET /overtime/pending` · `POST /overtime/:id/approve` · `/reject` | reviewer | same handlers as the panel |

## Security checklist

- Mini App: `initData` HMAC verified with the branch bots' tokens (constant-time), max age
  `MINIAPP_SESSION_TTL` (24h); identity = numeric Telegram user ID (usernames are
  informational only). 300 req/min/user, 20 check-in/out uploads/min/user.
- Bot tokens (one per branch): stored AES-256-GCM-encrypted on the branch (key derived
  from `JWT_SECRET`), so a leaked database backup doesn't leak them; never returned by
  the API or written to the audit log. Changing `JWT_SECRET` means re-entering each
  branch's token on its page.
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
admin panel APIs: initData validation, unregistered users, check-in/out (with and
without a photo) and reviewer notifications, manager scope, double check-in/out, photo
reuse, reject → re-submit, leave overlap/approval/leave-day blocking, half days (shift
halves, overlap rules, a morning off moving the start), special schedules for several
people, day close (absent vs weekend), the daily report, panel login + session cookie +
CSRF header, manual edit + audit, audit immutability, photo streaming, Excel export and
deactivation.

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

## Upgrading: overtime (migration 0012)

Migration `0012` adds the `overtime_requests` table. Nothing else changes; the Mini App
home gets an *Overtime* shortcut and the admin menu *Attendance → Overtime*.

## Upgrading: several managers per employee (migration 0011)

Migration `0011` moves each employee's manager into the new `employee_managers` table
and drops `employees.manager_id`; nothing changes for anyone. Give someone more
managers in the employee form. API clients can keep sending `manager_id` (one manager)
or send `manager_ids`; responses have `manager_ids` instead of `manager_id`.

## Upgrading: branch roles (migration 0010)

Migration `0010` adds `roles.branch_id` (a role can belong to one branch) and gives
**Branch Admins** *Admin accounts & roles of their branches* (`users.branch`): they now
see *Administration* — System users, Branch assignment and Role management — for their
own branch. Existing roles stay company roles. Clearing data (`clear-data`) removes the
branches' own roles with the branches.

## Upgrading: client admins manage their own admin accounts (migration 0009)

Migration `0009` gives the **Company Admin** role the new permission *Admin accounts of
their branches* (`users.branch`), so a client's admin can create and manage the admin
accounts of their own branches. Tick it on another role in *Role management* to give it
the same. List endpoints now page 10 at a time by default (see *REST API*); a script that
read whole lists should add `page_size=0`.

## Upgrading: everything per branch (migration 0008)

Migration `0008` (runs on start) moves the company-wide setup onto the branches:

- **Setup lists.** Departments, positions, shifts, leave types and holidays are copied
  into every branch, and each person is pointed at their own branch's copies (their
  leave requests and balances too), so nothing changes for anyone. Staff without a
  branch are put into the first branch. Afterwards edit each branch's lists on its own —
  delete what a branch doesn't use.
- **Telegram.** The bot token, Mini App URL and chats from *Settings → Telegram* are
  copied to every branch, so all branches keep working with the old bot. Give a branch
  its own bot on its page when you're ready (a new token there replaces the shared one
  for that branch only). A branch without its own group or review chat gets the
  company's; from then on each branch posts only to its own chats.
- **GPS.** The company GPS rules and office point are copied to every branch that didn't
  have its own; *Settings → Location (GPS)* is gone — set them on each branch's page.
- **Roles.** Branch admins also get *Manage schedules* and *Manage leave* (for their
  branch). A new **Company Admin** role (`company_admin`) is added for clients: it
  reaches the branches assigned to it or that it creates (*Create branches*).
- **Reasons.** `attendance` gets `check_in_reason` and `check_out_reason`; from now on a
  late check-in or early check-out needs one.

## Upgrading: system users and employees become separate

Migration `0007` (runs on start) sorts every existing admin panel login:

- An admin (owner, HR, branch admin, custom admin role) who **never checked in** becomes a
  system user as they are.
- An admin who **also checked in** is split in two: a new system user takes the login,
  role, branch access and branch-admin assignment; the employee record keeps the
  attendance, leave and Telegram link, as *Manager* if people report to them (they keep
  reviewing them in the Mini App), otherwise *Staff*. To get review notifications as the
  admin, give the system user its own Telegram ID (one Telegram account belongs to one
  record), or use the branch's review chat.
- A **manager's or staff member's** panel login is removed: managers review in the Mini
  App. Give them a system user if they need the panel.

What happened to whom is written to the audit log (`migration.system_users`).
`create-admin` now creates the owner as a system user (`-code` is no longer needed).

## Migrating from the chat-bot version

- Remove the old webhook so Telegram stops delivering messages to a URL that no longer
  exists: `curl -s https://api.telegram.org/bot<your-bot-token>/deleteWebhook`.
- Telegram moved from `.env` to the admin panel (now each branch's page → *Telegram*). These are no
  longer used — delete them from `.env`: `BOT_TOKEN`, `MINIAPP_URL`, `TELEGRAM_GROUP_ID`,
  `TELEGRAM_ADMIN_CHAT_ID` (now *Review chat ID*), `BACKUP_NOTIFY_CHAT_ID`, `CORS_ORIGINS`,
  `BOT_MODE`, `BOT_WEBHOOK_URL`, `TELEGRAM_WEBHOOK_SECRET`, `INTERNAL_API_TOKEN`.
- Migration `0003` adds branches and puts every existing non-admin employee into
  *Main branch*.
- Confirm/Reject buttons on notifications sent before the upgrade no longer do anything;
  review those items in the admin panel or the Mini App's Team tab.
- Existing employees, Telegram links, attendance, photos and leave are unchanged.


# A — add the demo data next to what's already there
railway ssh --service mini -- /app/api seed-demo -force

# B — wipe everything except the owner account, then load the demo
railway ssh --service mini -- /app/api clear-data -yes -force
railway ssh --service mini -- /app/api seed-demo -force
