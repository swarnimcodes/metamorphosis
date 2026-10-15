-- +goose Up
CREATE TABLE exercises (
       id    	       UUID		PRIMARY KEY DEFAULT uuidv7(),
       user_id	       UUID		NOT NULL REFERENCES users(id) ON DELETE CASCADE,
       name  	       TEXT      	NOT NULL,
       created_at      TIMESTAMPTZ	NOT NULL DEFAULT now(),
       updated_at      TIMESTAMPTZ	NOT NULL DEFAULT now(),
       UNIQUE (user_id, name)
);

-- +goose Down
DROP TABLE exercises;
