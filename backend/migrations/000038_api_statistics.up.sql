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

-- Retain aggregate counts from the old authenticated catalog traffic, then
-- discard request-level identity and timing records. New traffic includes anonymous calls.
INSERT INTO audit.api_statistics(day,service,method,route,status,requests,total_ms,max_ms)
SELECT (at AT TIME ZONE 'UTC')::date,'catalog',method,route,status,count(*),sum(ms),max(ms)
FROM catalog.api_request_logs GROUP BY 1,method,route,status
ON CONFLICT(day,service,method,route,status) DO UPDATE SET
 requests=api_statistics.requests+EXCLUDED.requests,
 total_ms=api_statistics.total_ms+EXCLUDED.total_ms,
 max_ms=GREATEST(api_statistics.max_ms,EXCLUDED.max_ms);
DROP TABLE catalog.api_request_logs;
UPDATE catalog.schema_contract SET version=38 WHERE singleton=true;
