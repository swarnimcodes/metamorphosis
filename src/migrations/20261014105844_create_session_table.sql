-- +goose Up
CREATE TABLE sessions (
       id    	       UUID		PRIMARY KEY DEFAULT uuidv7(),
       user_id	       UUID		NOT NULL REFERENCES users(id) ON DELETE CASCADE,
       place	       TEXT		NOT NULL,
       performed_at    TIMESTAMPTZ	NOT NULL DEFAULT now(),
       name  	       TEXT      	,
       created_at      TIMESTAMPTZ	NOT NULL DEFAULT now(),
       updated_at      TIMESTAMPTZ	NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE sessions;
