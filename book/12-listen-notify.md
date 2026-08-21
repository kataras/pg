# Chapter 12: LISTEN and NOTIFY

PostgreSQL has a built-in publish/subscribe channel, separate from
tables and rows: a connection issues `LISTEN channel`, and from then
on the server pushes it an asynchronous message every time any
connection runs `NOTIFY channel, 'payload'` (or the equivalent
`pg_notify` function), for as long as that connection stays open. pg
wraps this at two levels. The low-level API, `DB.Listen`, `DB.Notify`,
`DB.Unlisten` and the `Listener` type, is a thin, direct mapping onto
those SQL statements. The higher-level table-change API,
`PrepareListenTable` and `DB.ListenTable` (plus
`Repository[T].ListenTable`), installs a trigger and a shared notify
function so INSERT/UPDATE/DELETE on a table arrives as a typed
`TableNotification` instead of a hand-built payload string. This
chapter covers both layers and is honest about what LISTEN/NOTIFY is
not: a durable queue. A listener that is not connected when a
notification fires never sees it; there is no replay, no persistence,
and no delivery guarantee beyond "delivered to whoever was listening
at the time." Every signature below is read from `db.go`, `listener.go`
and `db_table_listener.go`.

## Table of Contents

- [Background](#background)
- [DB.Listen and Notification Delivery](#dblisten-and-notification-delivery)
- [DB.Notify](#dbnotify)
- [DB.Unlisten](#dbunlisten)
- [The Listener Type](#the-listener-type)
- [Notification, ErrEmptyPayload and UnmarshalNotification](#notification-erremptypayload-and-unmarshalnotification)
- [Quoted Channel Identifiers](#quoted-channel-identifiers)
- [The Table-Change Layer](#the-table-change-layer)
- [ListenTableOptions](#listentableoptions)
- [The Trigger and Function pg Installs](#the-trigger-and-function-pg-installs)
- [TableNotification and Change Kinds](#tablenotification-and-change-kinds)
- [Repository ListenTable](#repository-listentable)
- [The Identifier Restriction](#the-identifier-restriction)
- [Operational Caveats](#operational-caveats)
- [Summary](#summary)
- [Further Reading](#further-reading)

## Background

If you already know PostgreSQL's `LISTEN`/`NOTIFY` and why it is not a
message queue, skip to
[DB.Listen and Notification Delivery](#dblisten-and-notification-delivery).
`LISTEN channel_name` tells the current connection's backend process
to start delivering any notification sent to `channel_name`.
`NOTIFY channel_name, 'payload'` (or `SELECT pg_notify('channel_name',
'payload')`, the same operation as a callable function instead of a
statement) sends one such notification, containing an optional text
payload, to every connection currently listening on that channel, at
the moment `NOTIFY` runs. `UNLISTEN` stops delivery. The mechanism is
intentionally simple and has no memory: PostgreSQL does not store
notifications anywhere, does not know or care whether anyone received
one, and drops it immediately for any connection that was not
listening at that exact moment, including one that will reconnect and
issue `LISTEN` again a second later. This is the opposite trade-off
from a message broker like RabbitMQ or a durable log like Kafka,
which persist messages so a consumer that was offline can catch up.
LISTEN/NOTIFY is closer to a radio broadcast: useful for waking up
application code that is already listening ("a row changed, go check
it"), unsuitable for anything that must never lose a message.

## DB.Listen and Notification Delivery

```go
func (db *DB) Listen(ctx context.Context, channel string) (*Listener, error)
```

`Listen` acquires a dedicated connection from the pool, issues `LISTEN
<channel>` on it, and returns a `*Listener` bound to that connection:

```go
conn, err := db.Listen(context.Background(), "chat_db")
if err != nil {
    log.Fatal(err)
}
defer conn.Close(context.Background())

for {
    notification, err := conn.Accept(context.Background())
    if err != nil {
        log.Println(err)
        return
    }

    fmt.Printf("channel: %s, payload: %s\n",
        notification.Channel, notification.Payload)
}
```

The acquired connection is not returned to the pool until the
`Listener` is closed; see
[Operational Caveats](#operational-caveats) for what that costs.

## DB.Notify

```go
func (db *DB) Notify(ctx context.Context, channel string, payload any) error
```

`Notify` sends a notification via `pg_notify`. What it does with
`payload` depends on its Go type:

- `string` or `[]byte`: sent as-is, unmodified, as the raw payload.
- anything else: marshaled with `encoding/json/v2` first, then sent as
  the JSON text.

One consequence of the move from `encoding/json` to
`encoding/json/v2` is worth knowing if something other than Go reads
the channel: v2 does not HTML-escape `<`, `>` and `&`, so a payload
containing those characters now carries them literally instead of as
the six-character `\u`-escapes v1 emitted for them. Round-tripping
through pg is unaffected, since both spellings decode to the same
string; only a consumer inspecting the raw payload bytes, or diffing
them against a stored expectation, would notice.

```go
err := db.Notify(ctx, "chat_db", "hello") // sent as-is.

type Message struct {
    Sender string `json:"sender"`
    Body   string `json:"body"`
}

err = db.Notify(ctx, "chat_json", Message{Sender: "kataras", Body: "hi"})
// sent as: {"sender":"kataras","body":"hi"}
```

`Notify` always runs on `db.Pool` directly (not through `db.tx`, even
if `db` is transactional), since a notification sent from inside a
transaction that later rolls back would otherwise need PostgreSQL's
own deferred-delivery behavior (real `NOTIFY` only delivers at commit
if issued inside a transaction) to avoid notifying about work that
never happened; sending outside the transaction sidesteps that
question entirely by notifying immediately, regardless of whether any
surrounding transaction eventually commits.

## DB.Unlisten

```go
func (db *DB) Unlisten(ctx context.Context, channel string) error
```

`Unlisten` issues `UNLISTEN <channel>` on whichever connection the
pool happens to hand out for the call, which is not necessarily the
same connection a prior `Listen` call is subscribed on: pooled
connections are not addressable individually through `DB.Exec`. To
stop a specific `Listener`, call `Listener.Close` instead, which
issues `UNLISTEN` on that exact connection before releasing it. The
special channel name `"*"` unsubscribes the connection from every
channel it is currently listening on; `Unlisten` recognizes it and
emits it unquoted (as the keyword-like wildcard it is), rather than
quoting it as a literal channel named `*`.

## The Listener Type

```go
type Listener struct { /* unexported */ }

func (l *Listener) Accept(ctx context.Context) (*Notification, error)
func (l *Listener) Close(ctx context.Context) error
```

`Accept` blocks until a notification arrives on the subscribed
channel (or `ctx` is done), and returns it as a `*Notification`
(`pgconn.Notification` under a package-level alias, carrying
`Channel`, `Payload` and `PID`, the sending backend's process ID). If
the payload is empty, `Accept` returns `ErrEmptyPayload` instead of an
empty `*Notification`, since an empty payload is rarely meaningful and
callers otherwise have to remember to check `len(payload) == 0`
themselves on every notification.

`Close` unsubscribes and releases the underlying pooled connection. It
is safe to call more than once, and safe to call concurrently: only
the first call does any work (guarded by an atomic compare-and-swap),
every later call is a no-op returning `nil`. If the `UNLISTEN`
statement itself fails, `Close` closes the raw connection outright
(rather than releasing it back to the pool to be recycled), so pgxpool
destroys it instead of handing a connection that is still subscribed
to some channel to the next caller who acquires it for something
unrelated.

`Close` is also safe to call while another goroutine sits inside
`Accept`, which is the ordinary shape of a listener: one goroutine
loops on `Accept`, and something else stops it. That case needs more
than the compare-and-swap, because `Accept` and `Close` drive the same
`pgconn.PgConn`, and pgx documents that type as unsafe for concurrent
use. So `Close` cancels the in-flight wait first, and only once
`Accept` has handed the connection back does it run `UNLISTEN` and
release. The interrupted `Accept` returns `ErrListenerClosed`. Calling
`Accept` again after that returns `ErrListenerClosed` too, rather than
reading from a connection the pool has already given to somebody else.

## Notification, ErrEmptyPayload and UnmarshalNotification

```go
type Notification = pgconn.Notification // Channel, Payload, PID.

var ErrEmptyPayload = errors.New("empty payload")
var ErrListenerClosed = errors.New("listener closed")

func UnmarshalNotification[T any](n *Notification) (T, error)
```

`ErrListenerClosed` reports an orderly shutdown rather than a failure,
so a listen loop should return on it instead of logging it as an
error:

```go
for {
    notification, err := conn.Accept(ctx)
    if errors.Is(err, pg.ErrListenerClosed) {
        return // somebody called Close; nothing went wrong.
    }
    if err != nil {
        return fmt.Errorf("accept: %w", err)
    }
    // ...
}
```

`UnmarshalNotification` is the counterpart to `Notify`'s JSON-encoding
branch: it JSON-decodes `n.Payload` into `T` and returns it. It stays
a package-level function rather than becoming a method the way most of
the library's generic helpers have, because `Notification` is a type
alias for `pgconn.Notification` and Go does not allow methods on a
type declared in another package. It decodes with `encoding/json/v2`,
passing `json.MatchCaseInsensitiveNames(true)` so that a payload
produced by a row-to-JSON trigger, whose keys are PostgreSQL's
lower-cased column names, still lands in `T`'s fields when `T` is one
of your entities carrying `pg` tags rather than `json` ones. v2
matches names exactly by default; the option restores the behavior v1
gave you unconditionally.

```go
type Message struct {
    Sender string `json:"sender"`
    Body   string `json:"body"`
}

notification, err := conn.Accept(ctx)
if err != nil {
    log.Fatal(err)
}

payload, err := pg.UnmarshalNotification[Message](notification)
if err != nil {
    log.Fatal(err)
}

fmt.Println(payload.Sender, payload.Body)
```

Nothing enforces that a payload was actually produced by `Notify`'s
JSON branch. `UnmarshalNotification` against a plain string payload
(sent with `Notify(ctx, channel, "hello")`, or by any other client
issuing raw `NOTIFY`) fails with a JSON decode error unless `T` is
itself `string`, since `"hello"` alone is not valid JSON for a struct.

## Quoted Channel Identifiers

Both `Listen` and `Notify`/`Unlisten` quote the channel name before
embedding it in SQL, via `QuoteIdentifier`. This is not only an
injection guard; it also keeps `LISTEN` and `pg_notify` consistent
with each other for a mixed-case channel name. An unquoted identifier
in a `LISTEN` statement is folded to lowercase by PostgreSQL's normal
identifier rules, but `pg_notify`'s channel argument is an ordinary
string parameter, never folded at all. Without quoting on the `LISTEN`
side, `db.Listen(ctx, "ChatDB")` would subscribe to `chatdb`
(lowercased), while `db.Notify(ctx, "ChatDB", ...)` sends to the
literal channel `ChatDB`, and the two would simply never meet.
Quoting `LISTEN "ChatDB"` preserves the case exactly, so a caller who
only ever uses this package's own `Listen`/`Notify` pair (rather than
issuing raw `LISTEN`/`NOTIFY` SQL by hand) gets consistent behavior
regardless of the casing chosen for a channel name.

## The Table-Change Layer

Hand-writing a trigger, a notify function and a payload format for
every table you want to watch is repetitive. `PrepareListenTable` and
`DB.ListenTable` do it once, generically, for any set of registered
tables:

```go
func (db *DB) PrepareListenTable(
    ctx context.Context, opts *ListenTableOptions,
) error

func (db *DB) ListenTable(ctx context.Context, opts *ListenTableOptions,
    callback func(TableNotificationJSON, error) error) (Closer, error)
```

`ListenTable` calls `PrepareListenTable` internally (creating whatever
triggers and functions are missing), then calls `Listen` on the
resolved channel, then starts a goroutine that loops on `Accept`,
JSON-decodes each notification into a `TableNotificationJSON`, and
calls `callback` with it:

```go
opts := &pg.ListenTableOptions{
    Tables: map[string][]pg.TableChangeType{
        "customers": {pg.TableChangeTypeInsert, pg.TableChangeTypeUpdate},
    },
}

closer, err := db.ListenTable(ctx, opts,
    func(evt pg.TableNotificationJSON, err error) error {
        if err != nil {
            log.Println("listen table error:", err)
            return err // non-nil stops the listener.
        }

        log.Printf("table: %s, change: %s\n", evt.Table, evt.Change)
        return nil // nil keeps listening.
    })
if err != nil {
    log.Fatal(err)
}
defer closer.Close(ctx)
```

`callback` returning any non-nil error stops the listener (the
goroutine returns and the underlying `Listener` is closed via its own
`defer`); returning `nil` keeps it running. `PrepareListenTable` can
also be called on its own, ahead of time (for example during
deployment, alongside `CreateSchema`), to install the triggers without
immediately starting to listen.

## ListenTableOptions

```go
type ListenTableOptions struct {
    Tables   map[string][]TableChangeType
    Channel  string
    Function string
}
```

- `Tables`: which tables to watch, and which of `INSERT`/`UPDATE`/
  `DELETE` to notify on for each. The special key `"*"` means every
  base table registered in the schema, watching the changes named in
  its value; it defaults to `{"*": [INSERT, UPDATE, DELETE]}` when
  `Tables` is left empty entirely.
- `Channel`: the PostgreSQL channel to `LISTEN`/notify on. Defaults to
  `"table_change_notifications"`.
- `Function`: the base name for the shared PL/pgSQL notify function.
  Each table's trigger is named `<table>_<Function>`. Defaults to
  `"table_change_notify"`.

## The Trigger and Function pg Installs

`prepareListenTable` (the unexported worker `PrepareListenTable`
calls once per table) creates one shared function:

```sql
CREATE OR REPLACE FUNCTION table_change_notify() RETURNS trigger AS $$
    DECLARE
    payload text;
    channel text := 'table_change_notifications';

    BEGIN
    SELECT json_build_object('table', TG_TABLE_NAME, 'change', TG_OP,
                              'old', OLD, 'new', NEW)::text
    INTO payload;
    PERFORM pg_notify(channel, payload);
    IF (TG_OP = 'DELETE') THEN
        RETURN OLD;
    ELSE
        RETURN NEW;
    END IF;
END;
$$
LANGUAGE plpgsql;
```

and, per watched table, a trigger naming it:

```sql
CREATE OR REPLACE TRIGGER customers_table_change_notify
AFTER INSERT OR UPDATE
ON customers
FOR EACH ROW
EXECUTE FUNCTION table_change_notify();
```

Both the function name and the notify channel are baked into the
generated SQL text as literals, not parameters, which is exactly why
`Channel` and `Function` are restricted to safe identifiers (see
[The Identifier Restriction](#the-identifier-restriction)). Creation
is tracked per `*DB` (shared across every `*DB` cloned from the same
root, including transaction-scoped clones, via a mutex-guarded
`tableNotifyState`) so calling `ListenTable`, or `PrepareListenTable`,
more than once for the same function or table does not re-issue the
`CREATE OR REPLACE` statements needlessly, even from concurrent
callers.

## TableNotification and Change Kinds

```go
type TableNotification[T any] struct {
    Table  string
    Change TableChangeType
    New    T
    Old    T
}

type TableNotificationJSON = TableNotification[json.RawMessage]
```

`Change` is one of three `TableChangeType` string constants:
`TableChangeTypeInsert` (`"INSERT"`), `TableChangeTypeUpdate`
(`"UPDATE"`) and `TableChangeTypeDelete` (`"DELETE"`), matching
PostgreSQL's own `TG_OP` trigger variable verbatim. `New` is populated
for `INSERT` and `UPDATE` and is the zero value of `T` for `DELETE`;
`Old` is populated for `UPDATE` and `DELETE` and is the zero value of
`T` for `INSERT`. `TableNotificationJSON` is the generic instantiation
`DB.ListenTable`'s callback receives directly: `New`/`Old` stay as raw
`json.RawMessage` since the low-level API has no registered Go type to
decode them into, leaving that decision to the caller (or, for a
typed table, to `Repository[T].ListenTable` below). That is still
`encoding/json`'s `RawMessage`, not the v2-era
`jsontext.Value`, so code holding a `TableNotificationJSON` keeps
compiling unchanged; `encoding/json/v2` handles `RawMessage` as raw
JSON natively.
`(TableNotification[T]).GetPayload() string` returns the notification's
raw, undecoded text, useful for debugging a decode failure.

## Repository ListenTable

```go
func (repo *Repository[T]) ListenTable(ctx context.Context,
    callback func(TableNotification[T], error) error) (Closer, error)
```

`Repository[T].ListenTable` is `DB.ListenTable` fixed to exactly one
table, the repository's own, watching all three change types, and
decoding `New`/`Old` into `T` instead of leaving them as raw JSON:

```go
customers := pg.NewRepository[Customer](db)

closer, err := customers.ListenTable(ctx,
    func(evt pg.TableNotification[Customer], err error) error {
        if err != nil {
            return err
        }

        fmt.Printf("change: %s, old name: %s, new name: %s\n",
            evt.Change, evt.Old.Name, evt.New.Name)
        return nil
    })
```

Internally it calls `db.ListenTable` with `Tables: map[string][]TableChangeType{
repo.td.Name: {Insert, Update, Delete}}`, and its callback unmarshals
the raw `Old`/`New` JSON fields into `T` itself before calling the
caller's typed callback, so `evt.New` and `evt.Old` above arrive
already decoded as `Customer` values, not `json.RawMessage`.

## The Identifier Restriction

`ListenTableOptions.Channel`, `Function`, and every non-wildcard key
in `Tables`, must match `^[A-Za-z_][A-Za-z0-9_$]*$`: a bare
identifier, starting with a letter or underscore, no quotes, dots or
spaces. `PrepareListenTable` validates every one of them before
touching the database at all. The restriction exists because these
three values are not passed as bind parameters anywhere in this
layer: `Channel` is embedded both as a PL/pgSQL single-quoted string
literal (`channel text := '<Channel>';`) inside the generated function
body and as a raw `LISTEN` target; `Function` and each table name are
embedded as raw SQL identifiers in `CREATE FUNCTION`/`CREATE TRIGGER`
statements. A `Channel` value containing an unescaped single quote, or
a `Function` value containing a semicolon, would otherwise let a
caller-controlled string alter the generated DDL. This is stricter
than the low-level `DB.Listen`/`Notify`, which quote (rather than
reject) an arbitrary channel name via `QuoteIdentifier`; the
table-change layer rejects instead of quoting because these values are
interpolated into more than one syntactic position (a string literal
and an identifier), and no single quoting strategy is safe for both at
once.

## Operational Caveats

Three things are worth knowing before relying on LISTEN/NOTIFY in
production, none of them specific to pg, all of them inherited
directly from PostgreSQL's own design:

- **Payload size.** PostgreSQL caps a `NOTIFY` payload at
  approximately 8000 bytes; a larger payload is rejected by the
  server with an error. `Notify`'s JSON-encoding branch makes it easy
  to exceed this without noticing, since a struct with a few nested
  slices grows quickly. Keep the payload to an identifier (a row's
  primary key) and have the receiver query for the rest, rather than
  trying to carry a whole row image through the channel, especially
  for wide tables.
- **The connection is held for the listener's lifetime.** `Listen`
  acquires one pooled connection and does not release it until
  `Close` is called; that connection is unavailable to every other
  caller of `db.Pool` for as long as the `Listener` (or the goroutine
  behind `ListenTable`) is alive. A pool sized for request-serving
  traffic, with several long-lived listeners layered on top, can run
  out of connections faster than expected. Size the pool with the
  listener count in mind, or use a separate, dedicated pool for
  listening connections.
- **A disconnected listener misses messages, and pg does not
  reconnect for you.** If the connection underlying a `Listener` is
  dropped (a network blip, a server restart, the connection being
  killed), any notification sent while it was down is gone; there is
  nothing to replay it from. `ListenTable`'s internal loop recognizes
  `ErrListenerClosed` (the returned `Closer` was closed) and
  `io.ErrUnexpectedEOF`/`net.ErrClosed` (the connection went away
  underneath it) as an intentional close and simply ends the
  goroutine, but any other error is handed to
  `callback`, and it is `callback`'s decision whether to stop
  (returning non-nil) or keep looping (returning `nil`). Since
  `Accept` on an already-broken connection tends to fail immediately
  rather than block, a callback that always returns `nil` on error
  risks a tight loop of instant failures instead of a clean stop.
  There is no automatic re-subscription: a caller that wants
  resilience against dropped connections needs to detect the
  callback's error, return non-nil to stop the listener, and call
  `Listen`/`ListenTable` again to obtain a fresh connection and
  resume, accepting that whatever happened on the channel during the
  gap is unrecoverable.

## Summary

- LISTEN/NOTIFY is PostgreSQL's built-in pub/sub channel, not a
  durable queue: nothing is persisted, and a listener that is not
  connected at the moment of a `NOTIFY` never receives it.
- `DB.Listen` acquires a dedicated pooled connection and returns a
  `*Listener`; `Accept` blocks for the next notification,
  `ErrEmptyPayload` flags an empty one, and `Close` unsubscribes and
  releases the connection, safely callable more than once and while
  another goroutine waits in `Accept`, which then reports
  `ErrListenerClosed`.
- `DB.Notify` sends a `string`/`[]byte` payload as-is or JSON-encodes
  anything else; `UnmarshalNotification[T]` decodes a JSON payload
  back into `T`. Channel names are quoted with `QuoteIdentifier` so
  `LISTEN` and `pg_notify` agree on a mixed-case channel.
- `PrepareListenTable`/`DB.ListenTable` install one shared notify
  function and a per-table trigger, delivering typed
  `TableNotification` values over a background goroutine;
  `Repository[T].ListenTable` narrows that to one table and decodes
  `Old`/`New` into the repository's own struct type.
- `Channel`, `Function` and table names in `ListenTableOptions` are
  restricted to bare SQL identifiers, rejected rather than quoted,
  because they are interpolated into both a PL/pgSQL string literal
  and raw SQL identifiers.
- Watch payload size (about 8000 bytes), the pooled connection each
  listener holds for its entire lifetime, and the lack of automatic
  reconnection: a dropped listener must be re-established by the
  caller, and whatever happened on the channel during the gap is
  gone.

## Further Reading

- [PostgreSQL: NOTIFY](https://www.postgresql.org/docs/current/sql-notify.html):
  the statement, its payload size limit, and delivery semantics
  (including delivery timing relative to a wrapping transaction).
- [PostgreSQL: LISTEN](https://www.postgresql.org/docs/current/sql-listen.html):
  channel identifier folding, the behavior this chapter's quoting
  section works around.
- [PostgreSQL: Asynchronous Notification](https://www.postgresql.org/docs/current/libpq-notify.html):
  the underlying libpq-level mechanism pgx's `WaitForNotification`
  wraps.
- [PostgreSQL: CREATE TRIGGER](https://www.postgresql.org/docs/current/sql-createtrigger.html):
  `AFTER`, `FOR EACH ROW`, and the `TG_OP`/`TG_TABLE_NAME` variables
  the generated notify function reads.
- [pgconn.Notification](https://pkg.go.dev/github.com/jackc/pgx/v5/pgconn#Notification):
  the driver type `Notification` aliases, with `PID`, `Channel` and
  `Payload`.

---

**Next Chapter**: [Introspection and Code Generation](13-introspection-and-code-generation.md)
