-- +goose Up
CREATE TABLE sets (
       id    	       UUID		PRIMARY KEY DEFAULT uuidv7(),
       user_id	       UUID		NOT NULL REFERENCES users(id) ON DELETE CASCADE,
       session_id      UUID		NOT NULL REFERENCES sessions(id),
       exercise_id     UUID		NOT NULL REFERENCES exercises(id),
       position        INT		NOT NULL,
       rir	       INT		NOT NULL,
       created_at      TIMESTAMPTZ	NOT NULL DEFAULT now(),
       updated_at      TIMESTAMPTZ	NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE sets;
