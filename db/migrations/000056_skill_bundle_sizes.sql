ALTER TABLE personal_skills DROP CONSTRAINT personal_skills_file_count_check;
ALTER TABLE personal_skills DROP CONSTRAINT personal_skills_size_bytes_check;
ALTER TABLE personal_skills ALTER COLUMN size_bytes TYPE bigint;
ALTER TABLE personal_skills ADD CONSTRAINT personal_skills_file_count_check CHECK (file_count > 0);
ALTER TABLE personal_skills ADD CONSTRAINT personal_skills_size_bytes_check CHECK (size_bytes > 0);
