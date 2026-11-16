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

	title := func(p post) ai.Content { return ai.Text(p.Title) }
	got, err := ai.RerankBy(t.Context(), aitest.NewReranker(), ai.Text("Ruby"), posts, title, 1)
	require.NoError(t, err)
	assert.Equal(t, []post{{"Rails 与 Ruby"}}, got)

	got, err = ai.RerankBy(t.Context(), aitest.NewReranker(), ai.Text("Ruby"), nil, title, 1)
	require.NoError(t, err)
	assert.Empty(t, got)

	_, err = ai.RerankBy(t.Context(), aitest.NewReranker([]ai.Ranked{{Index: 5}}), ai.Text("PHP"), posts, title, 0)
	assert.ErrorContains(t, err, "rerank index 5 out of range")
}

func TestContent(t *testing.T) {
	png := ai.Media{Data: []byte("PNG"), MIME: "image/png"}
	c := ai.Content{ai.TextPart("标题"), ai.MediaPart(png), ai.TextPart("说明")}
	assert.Equal(t, "标题\n说明", c.Text())
	assert.Equal(t, []ai.Media{png}, c.Media())
	assert.Equal(t, []ai.Modality{ai.ModalityText, ai.ModalityImage}, c.Modalities())
	require.NoError(t, c.Validate())
	assert.Equal(t, ai.ModalityVideo, ai.Media{MIME: "video/mp4"}.Modality())
	assert.Equal(t, ai.Modality(""), ai.Media{MIME: "application/pdf"}.Modality())

	assert.ErrorContains(t, ai.Content{}.Validate(), "empty content")
	assert.ErrorContains(t, ai.Content{{Text: "a", Media: &png}}.Validate(), "both text and media")
	assert.ErrorContains(t, ai.Content{ai.MediaPart(ai.Media{MIME: "image/png"})}.Validate(), "needs media data")
	assert.ErrorContains(t, ai.Content{ai.TextPart("")}.Validate(), "is empty")

	texts, err := ai.Texts([]ai.Content{ai.Text("猫"), c[:1]})
	require.NoError(t, err)
	assert.Equal(t, []string{"猫", "标题"}, texts)
	_, err = ai.Texts([]ai.Content{ai.Text("猫"), c})
	assert.ErrorIs(t, err, ai.ErrUnsupportedInput)
	assert.ErrorContains(t, err, "input 1 has media")
}

func TestDataURL(t *testing.T) {
	png := ai.Media{Data: []byte("PNG"), MIME: "image/png"}
	assert.Equal(t, "data:image/png;base64,UE5H", png.DataURL())
	got, err := ai.ParseDataURL(png.DataURL())
	require.NoError(t, err)
	assert.Equal(t, png, got)

	got, err = ai.ParseDataURL("data:text/plain;charset=utf-8,%E4%BD%A0%E5%A5%BD")
	require.NoError(t, err)
	assert.Equal(t, ai.Media{Data: []byte("你好"), MIME: "text/plain"}, got)

	for _, bad := range []string{"image/png;base64,UE5H", "data:image/png;base64", "data:image/png;base64,***", "data:;;;,x"} {
		_, err := ai.ParseDataURL(bad)
		assert.ErrorContains(t, err, "malformed data URL", bad)
	}
}

func TestTopRanked(t *testing.T) {
	results := []ai.Ranked{{Index: 0, Score: 0.2}, {Index: 2, Score: 0.9}, {Index: 1, Score: 0.2}}
	got, err := ai.TopRanked(results, 3, 2)
	require.NoError(t, err)
	assert.Equal(t, []ai.Ranked{{Index: 2, Score: 0.9}, {Index: 0, Score: 0.2}}, got)
	assert.Equal(t, 0, results[0].Index, "results is not modified")

	got, err = ai.TopRanked(results, 3, 0)
	require.NoError(t, err)
	assert.Len(t, got, 3)

	_, err = ai.TopRanked(results, 2, 0)
	assert.ErrorContains(t, err, "rerank index 2 out of range")
}
