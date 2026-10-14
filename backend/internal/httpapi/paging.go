package httpapi

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// Lists are paged: ?page= (from 1) and ?page_size= — 10 by default, at most
// maxPageSize; page_size=0 returns everything (for pickers and selects). The Mini
// App's routes return everything unless they ask for a page.
const (
	defaultPageSize = 10
	maxPageSize     = 500
)

func pageParams(c *gin.Context) (page, size int) {
	page, _ = strconv.Atoi(c.Query("page"))
	if page < 1 {
		page = 1
	}
	size = defaultPageSize
	if strings.HasPrefix(c.FullPath(), "/api/v1/app/") {
		size = 0
	}
	if v := c.Query("page_size"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			size = min(n, maxPageSize)
		}
	}
	if size == 0 {
		page = 1
	}
	return page, size
}

// paged is one page of items with the paging fields: {items, total, page, page_size}.
func paged[T any](c *gin.Context, items []T) gin.H {
	page, size := pageParams(c)
	total := len(items)
	if size > 0 {
		from := min((page-1)*size, total)
		items = items[from:min(from+size, total)]
	}
	if items == nil {
		items = []T{} // [] in JSON, never null
	}
	return gin.H{"items": items, "total": total, "page": page, "page_size": size}
}
