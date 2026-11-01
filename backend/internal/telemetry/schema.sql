DO $api_statistics$
BEGIN
  PERFORM pg_advisory_xact_lock(740206);
  IF to_regclass('audit.api_statistics') IS NULL THEN
    CREATE SCHEMA IF NOT EXISTS audit;
    CREATE TABLE audit.api_statistics (
      day date NOT NULL,
      service text NOT NULL,
      method text NOT NULL,
      route text NOT NULL,
      status integer NOT NULL,
      requests bigint NOT NULL,
      total_ms bigint NOT NULL,
      max_ms bigint NOT NULL,
      PRIMARY KEY(day,service,method,route,status)
    );
  END IF;
END
$api_statistics$;
