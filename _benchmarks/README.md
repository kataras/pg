# Benchmarks

Execute the following SQL query to create the table:

```sql
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- ----------------------------
-- Table structure for customers
-- ----------------------------
DROP TABLE IF EXISTS "public"."customers";
CREATE TABLE "public"."customers" (
  "id" uuid NOT NULL DEFAULT gen_random_uuid(),
  "created_at" timestamp(6) NOT NULL DEFAULT clock_timestamp(),
  "updated_at" timestamp(6) NOT NULL DEFAULT clock_timestamp(),
  "cognito_user_id" uuid NOT NULL
);

-- ----------------------------
-- Triggers structure for table customers
-- ----------------------------
CREATE TRIGGER "set_timestamp" BEFORE UPDATE ON "public"."customers"
FOR EACH ROW
EXECUTE PROCEDURE "public"."trigger_set_timestamp"();

-- ----------------------------
-- Uniques structure for table customers
-- ----------------------------
ALTER TABLE "public"."customers" ADD CONSTRAINT "customers_cognito_user_id_key" UNIQUE ("cognito_user_id");

-- ----------------------------
-- Primary Key structure for table customers
-- ----------------------------
ALTER TABLE "public"."customers" ADD CONSTRAINT "customers_pkey" PRIMARY KEY ("id");
```

Run the benchmarks:

```sh
$ go test -bench=BenchmarkDB_InsertSingle_Gorm -count 6 | tee result_gorm.txt
$ go test -bench=BenchmarkDB_InsertSingle_Pg -count 6 | tee result_pg.txt
$ # Check the runtime inside these files and the ns/op numbers or:
$ go install golang.org/x/perf/cmd/benchstat@latest
$ benchstat result_gorm.txt result_pg.txt
```
