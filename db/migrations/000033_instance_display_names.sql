-- Instance names are display labels; runtime identifiers remain UUID-derived.
ALTER TABLE instances DROP CONSTRAINT instances_name_check;
ALTER TABLE instances ADD CONSTRAINT instances_name_check
 CHECK (char_length(name) BETWEEN 1 AND 32 AND name ~ '^[[:alnum:]_-]+$');
