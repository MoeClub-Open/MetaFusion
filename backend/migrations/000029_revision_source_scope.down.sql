DO $$ BEGIN
    RAISE EXCEPTION '000029_revision_source_scope is irreversible: restore database and matching server together';
END $$;
