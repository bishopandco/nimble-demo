package api

import (
	"fmt"
	"net/mail"
	"strings"
	"unicode/utf8"

	"go-api/internal/model"
)

func text(value *string, name string, max int) error {
	*value = strings.TrimSpace(*value)
	length := utf8.RuneCountInString(*value)
	if length == 0 || length > max {
		return fmt.Errorf("%s must contain between 1 and %d characters", name, max)
	}
	return nil
}

func validateUser(in *model.UserInput) error {
	if err := text(&in.Name, "name", 100); err != nil {
		return err
	}
	in.Email = strings.ToLower(strings.TrimSpace(in.Email))
	address, err := mail.ParseAddress(in.Email)
	if err != nil || address.Address != in.Email || len(in.Email) > 254 || !strings.Contains(in.Email, "@") {
		return fmt.Errorf("email must be a valid email address")
	}
	return nil
}

func validateBlog(in *model.BlogInput) error {
	if err := text(&in.Title, "title", 200); err != nil {
		return err
	}
	if err := text(&in.Body, "body", 100_000); err != nil {
		return err
	}
	if in.AuthorID <= 0 {
		return fmt.Errorf("author_id must be a positive integer")
	}
	if len(in.TagIDs) > 100 {
		return fmt.Errorf("tag_ids must contain at most 100 IDs")
	}
	seen := make(map[int64]bool, len(in.TagIDs))
	for _, id := range in.TagIDs {
		if id <= 0 || seen[id] {
			return fmt.Errorf("tag_ids must contain unique positive integers")
		}
		seen[id] = true
	}
	return nil
}

func validateComment(in *model.CommentInput) error {
	if in.BlogID <= 0 || in.AuthorID <= 0 {
		return fmt.Errorf("blog_id and author_id must be positive integers")
	}
	return text(&in.Body, "body", 10_000)
}

func validateTag(in *model.TagInput) error {
	return text(&in.Name, "name", 50)
}
