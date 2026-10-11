DO $$ BEGIN
    RAISE EXCEPTION '000028_catalog_shared_budget is irreversible: restore database and matching server together';
END $$;
