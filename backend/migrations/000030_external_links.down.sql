DO $$ BEGIN
    RAISE EXCEPTION '000030_external_links is irreversible: restore database and matching server together';
END $$;
