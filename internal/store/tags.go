package store

import (
	"context"

	"go-api/internal/model"
)

func (s *Store) ListTags(ctx context.Context, opts ListOptions) ([]model.Tag, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT id, name FROM tags ORDER BY id LIMIT ? OFFSET ?", opts.Limit, opts.Offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tags := []model.Tag{}
	for rows.Next() {
		var tag model.Tag
		if err := rows.Scan(&tag.ID, &tag.Name); err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	return tags, rows.Err()
}

func (s *Store) GetTag(ctx context.Context, id int64) (model.Tag, error) {
	var tag model.Tag
	err := s.db.QueryRowContext(ctx, "SELECT id, name FROM tags WHERE id = ?", id).Scan(&tag.ID, &tag.Name)
	return tag, databaseError(err)
}

func (s *Store) CreateTag(ctx context.Context, in model.TagInput) (model.Tag, error) {
	var tag model.Tag
	err := s.db.QueryRowContext(ctx, "INSERT INTO tags (name) VALUES (?) RETURNING id, name", in.Name).Scan(&tag.ID, &tag.Name)
	return tag, databaseError(err)
}

func (s *Store) UpdateTag(ctx context.Context, id int64, in model.TagInput) (model.Tag, error) {
	var tag model.Tag
	err := s.db.QueryRowContext(ctx, "UPDATE tags SET name = ? WHERE id = ? RETURNING id, name", in.Name, id).Scan(&tag.ID, &tag.Name)
	return tag, databaseError(err)
}

func (s *Store) DeleteTag(ctx context.Context, id int64) error {
	return changed(s.db.ExecContext(ctx, "DELETE FROM tags WHERE id = ?", id))
}
