DROP INDEX comments_author_idx;
CREATE INDEX comments_author_idx ON comments (author_id);
