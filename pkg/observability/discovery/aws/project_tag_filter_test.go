// Tests for ProjectTagFilter, relocated here from
// pkg/observability/filter so the filter package stays SDK-free
// (reliable#2141 / #2153 — see project_tag_filter.go).

package aws

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProjectTagFilter(t *testing.T) {
	t.Parallel()
	t.Run("empty project returns nil", func(t *testing.T) {
		t.Parallel()
		assert.Nil(t, ProjectTagFilter(""))
	})
	t.Run("non-empty project returns tag:Project filter", func(t *testing.T) {
		t.Parallel()
		filters := ProjectTagFilter("io-myproject")
		assert.Len(t, filters, 1)
		assert.Equal(t, "tag:Project", *filters[0].Name)
		assert.Equal(t, []string{"io-myproject"}, filters[0].Values)
	})
}
