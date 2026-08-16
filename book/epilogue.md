# Epilogue

Sixteen chapters ago this book promised a path from an empty `go.mod` to
a tested, production-shaped data layer. Here at the end, it is worth
naming what you actually carry out of it, because it is more than a list
of function signatures.

## What You Can Do Now

You can take a set of Go structs, tag them with `pg:"..."`, register them
in a `Schema`, and both create and verify the tables they describe
against a live database, so a drifted schema fails loudly at startup
instead of quietly at 2 a.m. You can read and write through a typed
`Repository[T]` without hand-writing SQL for the common cases, and drop
down to the `Where` builder, raw `Query`, or a hand-written statement the
moment a case stops being common. You know when to reach for a
multi-row `InsertMany` or `CopyFrom` instead of a loop of single-row
inserts, and what each one costs you in return: `COPY` is faster, and it
gives up per-row `DEFAULT` and per-row constraint errors. You can wrap a
unit of work in `InTransaction`, tell a transient serialization failure
apart from a real one, and retry only the former. You can subscribe to a
table's changes with `ListenTable` instead of polling it, and you can
point `gen` at a live database to get Go structs back, or at an already
registered `Schema` to get typed column constants.

Just as importantly, you know what pg does *for* you: the prepared
statement cache and description cache it maintains so repeated queries
do not re-plan, the connection pool it hands you rather than a bare
connection, and the specific `SQLSTATE` each constraint violation maps
to so your error handling can branch on `ConstraintKind` instead of
matching a driver's error string. Good libraries are measured by the
footguns they remove; a fair amount of this book has quietly been about
those.

## The Habits Worth Keeping

If the details fade, keep the habits. They were the real curriculum:

- **Verify the schema, do not assume it.** `CheckSchema` costs one round
  trip at startup and catches the migration that never ran.
- **Branch on the constraint, not the message.** `AsConstraintError` and
  `ConstraintKind` survive a PostgreSQL upgrade that changes wording;
  a substring match on `err.Error()` does not.
- **Choose the write path on purpose.** A single `InsertSingle`, a
  batched `InsertMany`, and a streaming `CopyFrom` all insert rows; which
  one is correct depends on row count and on whether you need per-row
  defaults and per-row errors back.
- **Retry the failure that is actually transient.** `IsErrRetryableTx`
  exists because not every transaction error should be retried, and
  retrying the wrong one turns a bug into a silent duplicate write.
- **Let the pool outlive the request.** Open one `*pg.DB` per process,
  not per handler, and let its pool amortize the cost of a new
  connection across everything that runs after it.

None of these are pg-specific. They will serve you against any database
library you use next. pg simply gives each one a direct, typed way to
follow it.

## What to Build Next

Reading builds understanding; building makes it stick. Three
suggestions, in rising order of ambition:

1. **Rebuild one of the `_examples` programs from memory.** The
   `password` example wires up hashing and the `gen` package end to end;
   doing it again without looking is the fastest way to find the parts
   that have not settled yet.
2. **Wire `ListenTable` into something that watches.** The `live-table`
   example pushes row changes to a browser over a websocket; a Slack
   notification or a cache invalidation is a smaller version of the same
   idea.
3. **Port one table of a real project to pg.** Take a table you already
   query by hand and give it a struct, a registered schema and a
   `Repository[T]`. That is the moment the material becomes yours.

## Staying Current

- The library lives at
  [github.com/kataras/pg](https://github.com/kataras/pg): releases,
  issues and discussions all happen there.
- API reference documentation is on
  [pkg.go.dev/github.com/kataras/pg](https://pkg.go.dev/github.com/kataras/pg),
  generated from the same source you have been reading about.
- Runnable programs beyond this book's examples live in the repository's
  `_examples` directory, including the `logging` example, which wires pg's
  query tracing into a structured logger.

## Contributing

Bug reports with a failing test, documentation fixes, and honest
questions in the discussions all move the library forward, the same way
they move any open source project forward. The same goes for this book:
when you spot something that could be clearer, or wrong, say so in the
repository's discussions. Corrections from readers are how a book stays
trustworthy.

## Thank You

A database library is a wager: that the hours spent getting the schema,
the pool and the query builder right will save many more hours for
everyone who writes application code on top of them. By reading this
far, and by whatever you build next, you are the other side of that
wager paying off.

Thank you for reading. Now go build something good.

- Gerasimos Maropoulos
