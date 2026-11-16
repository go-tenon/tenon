package vector

import (
	"context"
	"fmt"

	"github.com/go-tenon/tenon/doctor"
)

const docVector = "docs/ai/vector.md#reindexing"

// Doctor 返回检查向量索引的一组自检，组名为 vector：
//
//   - qdrant：Qdrant 连得上；
//   - 每个索引一项，名称是索引名：索引正在使用的版本（和正在重建的版本）是用 Store 配置的模型建立的。
//     建立它的模型没有配置时失败，例如换了模型却没有用 WithPrevious 传入旧模型，查询会出错；
//     仍是用 WithPrevious 传入的旧模型建立、又没有在重建时提示：重建之后才能去掉旧模型。还没有建立的索引通过。
//
// indexes 是要检查的索引名，即 Index.Name()。s 在检查时才使用。
func Doctor(s *Store, indexes ...string) doctor.Group {
	checks := []doctor.Check{{Name: "qdrant", Run: func(ctx context.Context) error {
		if _, err := s.client.HealthCheck(ctx); err != nil {
			return doctor.Fail("cannot reach Qdrant: "+err.Error(),
				"check the Qdrant address (the gRPC port, 6334 by default) and the API key").See("docs/ai/vector.md")
		}
		return nil
	}}}
	for _, index := range indexes {
		checks = append(checks, doctor.Check{Name: index, Run: func(ctx context.Context) error {
			return s.checkIndex(ctx, index)
		}})
	}
	return doctor.Group{Name: "vector", Checks: checks}
}

// checkIndex 检查索引的各个版本是用哪个模型建立的。
func (s *Store) checkIndex(ctx context.Context, index string) error {
	var migrated bool
	if err := s.db.NewRaw("SELECT to_regclass('vector_indexes') IS NOT NULL").Scan(ctx, &migrated); err != nil {
		return err
	}
	if !migrated {
		return doctor.Skip("the vector migrations have not been applied yet")
	}
	st, err := readState(ctx, s.db, index, false)
	if err != nil {
		return err
	}
	versions := []int{st.Version}
	if st.Building.Valid {
		versions = append(versions, int(st.Building.Int64))
	}
	current := modelID{s.model.Name, s.model.Dims}
	var built []modelID // 各版本的模型，与 versions 对应；还没有建立的版本不在其中
	for _, v := range versions {
		info, err := s.client.GetCollectionInfo(ctx, s.collection(index, v))
		if notFound(err) {
			continue
		}
		if err != nil {
			return err
		}
		built = append(built, builtWith(info))
	}
	return checkModels(index, current, s.models, built, st.Building.Valid)
}

// checkModels 判断索引的各个版本的模型：都要是 configured 中的模型；没有在重建、却仍是旧模型时提示。
func checkModels(index string, current modelID, configured map[modelID]Model, built []modelID, building bool) error {
	for _, m := range built {
		if _, ok := configured[m]; !ok {
			return doctor.Fail(
				fmt.Sprintf("index %s was built with model %q (%d dimensions), which is not configured; queries on it fail",
					index, m.name, m.dims),
				fmt.Sprintf("pass that model with vector.WithPrevious until `search reindex %s` finishes", index),
			).See(docVector)
		}
	}
	if !building && len(built) > 0 && built[0] != current {
		return doctor.Warn(
			fmt.Sprintf("index %s still uses the previous model %q (%d dimensions)", index, built[0].name, built[0].dims),
			fmt.Sprintf("run `search reindex %s`; when it finishes, remove the old model from vector.WithPrevious", index),
		).See(docVector)
	}
	return nil
}
