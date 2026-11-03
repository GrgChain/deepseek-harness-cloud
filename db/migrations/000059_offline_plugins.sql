ALTER TABLE personal_plugins
 ADD COLUMN addition_method text NOT NULL DEFAULT 'online' CHECK(addition_method IN ('online','offline')),
 ADD COLUMN archive_filename text NOT NULL DEFAULT '',
 ADD COLUMN archive_sha256 text NOT NULL DEFAULT '',
 ADD COLUMN archive_key_id text,
 ADD COLUMN archive_nonce bytea,
 ADD COLUMN archive_ciphertext bytea,
 ADD CONSTRAINT personal_plugins_archive CHECK (
  (addition_method='online' AND archive_filename='' AND archive_sha256=''
   AND archive_key_id IS NULL AND archive_nonce IS NULL AND archive_ciphertext IS NULL)
  OR (addition_method='offline' AND length(archive_filename) BETWEEN 1 AND 255
   AND archive_sha256 ~ '^[a-f0-9]{64}$' AND archive_key_id IS NOT NULL AND length(archive_key_id) BETWEEN 1 AND 100
   AND archive_nonce IS NOT NULL AND octet_length(archive_nonce)=12
   AND archive_ciphertext IS NOT NULL AND octet_length(archive_ciphertext) BETWEEN 17 AND 5242896));
