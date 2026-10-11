DO $$ BEGIN
    RAISE EXCEPTION '000027_catalog_retention is irreversible: restore database and matching server together';
END $$;
