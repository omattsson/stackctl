-- CI fixture seed for the live integration suite.
--
-- The live tests gate themselves behind require{Cluster,Definition,Template}
-- helpers that skip when the backend has nothing to look at. A fresh CI
-- backend would skip ~70% of the suite — defeating the point of running it.
--
-- This script inserts the minimum metadata needed so every test reaches its
-- assertions:
--   - 1 cluster (no real connectivity)
--   - 1 stack_definition + 1 chart_config
--   - 1 stack_template + 1 template_chart_config
--
-- Owner is the env-seeded admin user (created by the backend's
-- EnsureAdminUser at boot). We look it up at apply time so we don't need to
-- substitute the non-deterministic UUID into the script.

SET @admin_id = (SELECT id FROM users WHERE username = 'admin' LIMIT 1);

-- Cluster — health_status stays "unreachable" since api-only mode has no
-- real kubeconfig. test-connection / nodes endpoints will fail; tests
-- handle that as expected.
INSERT INTO clusters (
    id, name, description,
    api_server_url, kubeconfig_data, kubeconfig_path,
    region, health_status,
    registry_url, registry_username, registry_password, image_pull_secret_name,
    max_namespaces, max_instances_per_user,
    is_default, use_in_cluster,
    created_at, updated_at
) VALUES (
    '00000000-0000-0000-0000-000000000001', 'ci-stub', 'CI fixture cluster — no real connectivity',
    '', '', '',
    '', 'unreachable',
    '', '', '', '',
    0, 0,
    1, 0,
    NOW(), NOW()
);

-- Stack definition (owner = admin) with a single noop chart.
INSERT INTO stack_definitions (
    id, name, description, owner_id, default_branch, created_at, updated_at
) VALUES (
    '00000000-0000-0000-0000-000000000002',
    'ci-stub-definition',
    'CI fixture definition — wire-shape only, never deployed',
    @admin_id, 'master', NOW(), NOW()
);

INSERT INTO chart_configs (
    id, stack_definition_id,
    chart_name, repository_url, source_repo_url, build_pipeline_id,
    chart_path, chart_version, default_values, deploy_order,
    created_at
) VALUES (
    '00000000-0000-0000-0000-000000000003',
    '00000000-0000-0000-0000-000000000002',
    'ci-noop', '', '', '',
    '', '0.1.0', '', 0,
    NOW()
);

-- Published stack template so requireTemplate and TestLiveTemplate_ListAndGet
-- find at least one row. Inline chart so the chart-roundtrip tests have
-- something to read.
INSERT INTO stack_templates (
    id, name, description, category, version, owner_id,
    default_branch, is_published, created_at, updated_at
) VALUES (
    '00000000-0000-0000-0000-000000000004',
    'ci-stub-template',
    'CI fixture template — published so list filter sees it',
    '', '1.0.0', @admin_id,
    'master', 1, NOW(), NOW()
);

INSERT INTO template_chart_configs (
    id, stack_template_id,
    chart_name, repository_url, source_repo_url, build_pipeline_id,
    chart_path, chart_version, default_values, locked_values,
    deploy_order, required,
    created_at
) VALUES (
    '00000000-0000-0000-0000-000000000005',
    '00000000-0000-0000-0000-000000000004',
    'ci-noop', '', '', '',
    '', '0.1.0', '', '',
    0, 0,
    NOW()
);
