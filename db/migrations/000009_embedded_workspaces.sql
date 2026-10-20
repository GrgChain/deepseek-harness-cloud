-- Coder v2.37.4 workspace/template/build persistence adapted to platform PG.
-- Modified 2026-10-13. SPDX-License-Identifier: AGPL-3.0-only
-- Upstream queries: coderd/database/queries/{workspaces,workspacebuilds,templateversions}.sql
CREATE TABLE workspace_authority (
 singleton boolean PRIMARY KEY DEFAULT true CHECK(singleton),
 organization_id uuid NOT NULL,
 control_user_id uuid NOT NULL
);
CREATE TABLE workspace_templates (
 id uuid PRIMARY KEY,
 organization_id uuid NOT NULL,
 name text NOT NULL CHECK(name='hc-dsh-initial'),
 active_version_id uuid NOT NULL,
 deleted boolean NOT NULL DEFAULT false,
 UNIQUE(organization_id,name)
);
CREATE TABLE workspace_template_versions (
 id uuid PRIMARY KEY,
 template_id uuid NOT NULL REFERENCES workspace_templates(id),
 organization_id uuid NOT NULL,
 name text NOT NULL,
 file_id uuid NOT NULL UNIQUE,
 source jsonb NOT NULL CHECK(jsonb_typeof(source)='object'),
 source_sha256 text NOT NULL CHECK(source_sha256 ~ '^[a-f0-9]{64}$'),
 created_by uuid NOT NULL,
 archived boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(template_id,name)
);
ALTER TABLE workspace_templates ADD CONSTRAINT workspace_template_active_fk FOREIGN KEY(active_version_id) REFERENCES workspace_template_versions(id) DEFERRABLE INITIALLY DEFERRED;
CREATE TABLE workspace_workspaces (
 id uuid PRIMARY KEY,
 name text NOT NULL CHECK(name ~ '^hc-[a-z2-7]{26}$'),
 owner_id uuid NOT NULL REFERENCES users(coder_user_id),
 organization_id uuid NOT NULL,
 template_id uuid NOT NULL REFERENCES workspace_templates(id),
 deleted boolean NOT NULL DEFAULT false,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 UNIQUE(owner_id,name)
);
CREATE TABLE workspace_builds (
 id uuid PRIMARY KEY,
 workspace_id uuid NOT NULL REFERENCES workspace_workspaces(id),
 template_version_id uuid NOT NULL REFERENCES workspace_template_versions(id),
 build_number integer NOT NULL CHECK(build_number>0),
 transition text NOT NULL CHECK(transition IN ('start','stop','delete')),
 initiator_id uuid NOT NULL,
 job_id uuid NOT NULL UNIQUE,
 job_status text NOT NULL DEFAULT 'pending' CHECK(job_status IN ('pending','running','succeeded','failed','canceled')),
 error_code text NOT NULL DEFAULT '',
 parameters jsonb NOT NULL CHECK(jsonb_typeof(parameters)='array'),
 provisioner_state bytea,
 created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
 started_at timestamptz,
 completed_at timestamptz,
 UNIQUE(workspace_id,build_number)
);
CREATE UNIQUE INDEX workspace_one_active_build ON workspace_builds(workspace_id) WHERE job_status IN ('pending','running');
REVOKE ALL ON workspace_authority,workspace_templates,workspace_template_versions,workspace_workspaces,workspace_builds FROM PUBLIC;
