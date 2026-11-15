-- Approach paths: the ways vehicles reached each hazard, so the search can
-- follow the road around curves. See docs/design.md, sections 5, 7 and 9.
-- The rules below are checked by database/hazard_approach_paths_table_test.go.

-- +goose Up

-- One row per stored path. A path is location history, so this table has no
-- device and no time on purpose (section 7). "The oldest path" is the one
-- with the lowest id.
CREATE TABLE hazard_approach_paths (
    id         BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    hazard_id  BIGINT NOT NULL,
    path       GEOGRAPHY(LineString, 4326) NOT NULL,  -- points oldest first, ending at the report

    CONSTRAINT hazard_approach_paths_hazard_id_fkey FOREIGN KEY (hazard_id) REFERENCES hazards (id)
);

-- "Which paths does this hazard have?" on every report that carries a trail.
CREATE INDEX hazard_approach_paths_hazard_id_index ON hazard_approach_paths (hazard_id);
-- "Which paths pass near this vehicle?" for the road-following search (C-118).
CREATE INDEX hazard_approach_paths_path_index ON hazard_approach_paths USING GIST (path);

-- How many paths each device has added to each hazard, for the limit per
-- device (section 5). It is a separate table so that a path row itself never
-- names a device. The limit's value lives in Go, next to the other limits.
CREATE TABLE hazard_approach_path_counts (
    hazard_id    BIGINT NOT NULL,
    device_id    TEXT NOT NULL,
    paths_added  INT NOT NULL,

    CONSTRAINT hazard_approach_path_counts_pkey PRIMARY KEY (hazard_id, device_id),
    CONSTRAINT hazard_approach_path_counts_hazard_id_fkey FOREIGN KEY (hazard_id) REFERENCES hazards (id),
    CONSTRAINT hazard_approach_path_counts_device_id_fkey FOREIGN KEY (device_id) REFERENCES devices (device_id),

    -- A row only exists once a device has added a path.
    CONSTRAINT hazard_approach_path_counts_paths_added_check CHECK (paths_added >= 1)
);
