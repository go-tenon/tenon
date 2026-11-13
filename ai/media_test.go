package ai_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/go-tenon/tenon/ai"
	"github.com/go-tenon/tenon/ai/aitest"
)

func TestRerankBy(t *testing.T) {
	type post struct{ Title string }
	posts := []post{{"Django 入门"}, {"Rails 与 Ruby"}, {"React Hooks"}}

	got, err := ai.RerankBy(t.Context(), aitest.NewReranker(), "Ruby", posts, func(p post) string { return p.Title }, 1)
	require.NoError(t, err)
	assert.Equal(t, []post{{"Rails 与 Ruby"}}, got)

	got, err = ai.RerankBy(t.Context(), aitest.NewReranker(), "Ruby", nil, func(p post) string { return p.Title }, 1)
	require.NoError(t, err)
	assert.Empty(t, got)

	_, err = ai.RerankBy(t.Context(), aitest.NewReranker([]ai.Ranked{{Index: 5}}), "PHP", posts, func(p post) string { return p.Title }, 0)
	assert.ErrorContains(t, err, "rerank index 5 out of range")
}
