INSERT INTO permissions(id,assignable,dependencies) VALUES
 ('skills:read',true,'{}'),('skills:manage',true,'{skills:read}');
INSERT INTO role_permissions(role_id,permission_id)
 SELECT r.id,p.id FROM roles r CROSS JOIN permissions p WHERE r.builtin AND p.id IN ('skills:read','skills:manage');

CREATE TABLE personal_skills (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL REFERENCES organizations(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 name text NOT NULL CHECK(length(name) BETWEEN 1 AND 64),
 description text NOT NULL CHECK(length(description) BETWEEN 1 AND 2000),
 model_invocable boolean NOT NULL,
 user_invocable boolean NOT NULL,
 version bigint NOT NULL DEFAULT 1 CHECK(version>0),
 file_count integer NOT NULL CHECK(file_count BETWEEN 1 AND 128),
 size_bytes integer NOT NULL CHECK(size_bytes BETWEEN 1 AND 12582912),
 key_id text NOT NULL, nonce bytea NOT NULL, ciphertext bytea NOT NULL,
 source_url text NOT NULL DEFAULT '',
 source_digest text NOT NULL DEFAULT '',
 synced_at timestamptz,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(org_id,owner_id,name)
);
CREATE INDEX personal_skills_owner ON personal_skills(org_id,owner_id,updated_at DESC,id);

-- Import previews are encrypted, short-lived and scoped to the requesting owner.
CREATE TABLE skill_import_previews (
 id uuid PRIMARY KEY,
 org_id uuid NOT NULL REFERENCES organizations(id),
 owner_id uuid NOT NULL REFERENCES users(id),
 key_id text NOT NULL, nonce bytea NOT NULL, ciphertext bytea NOT NULL,
 expires_at timestamptz NOT NULL DEFAULT clock_timestamp()+interval '5 minutes'
);
CREATE INDEX skill_import_expiry ON skill_import_previews(expires_at);

-- Keep the previous package when an import or sync explicitly replaces a Skill.
CREATE TABLE skill_replacements (
 skill_id uuid PRIMARY KEY REFERENCES personal_skills(id) ON DELETE CASCADE,
 key_id text NOT NULL, nonce bytea NOT NULL, ciphertext bytea NOT NULL,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

-- Fresh organizations receive the same personal-library permissions.
CREATE OR REPLACE FUNCTION public.hc_seed_roles() RETURNS trigger
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog,public AS $$
DECLARE admin_id uuid:=gen_random_uuid(); member_id uuid:=gen_random_uuid();
BEGIN
 INSERT INTO public.roles(id,org_id,code,name,builtin) VALUES
 (admin_id,NEW.id,'ADMIN','管理员',true),(member_id,NEW.id,'MEMBER','成员',true);
 INSERT INTO public.role_permissions SELECT admin_id,id FROM public.permissions;
 INSERT INTO public.role_permissions SELECT member_id,id FROM public.permissions
 WHERE id LIKE 'instances:%' OR id LIKE 'personal-model:%' OR id LIKE 'skills:%' OR id IN ('configuration:read','audit:read');
 RETURN NEW;
END;
$$;
