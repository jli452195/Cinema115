package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"strm115/backend/internal/naming"
	"strm115/backend/internal/storage"
)

// RegisterNamingTemplates is independent of cloud providers and media services.
// Preview only formats supplied facts; it never looks up or modifies files.
func RegisterNamingTemplates(admin *gin.RouterGroup, store *storage.Store) {
	group := admin.Group("/naming")
	group.Use(func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 256<<10)
		ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
		defer cancel()
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	})
	group.GET("", func(c *gin.Context) {
		records := make(map[naming.Kind]naming.Snapshot, 2)
		defaults := make(map[naming.Kind]naming.Template, 2)
		catalog := make(map[naming.Kind][]naming.FieldDefinition, 2)
		for _, kind := range []naming.Kind{naming.Movie, naming.TV} {
			record, err := store.NamingTemplate(c.Request.Context(), kind)
			if err != nil {
				namingError(c, err)
				return
			}
			records[kind] = record
			defaults[kind], _ = naming.Default(kind)
			catalog[kind], _ = naming.Catalog(kind)
		}
		c.JSON(200, gin.H{"records": records, "defaults": defaults, "catalog": catalog})
	})
	group.PUT("/:kind", func(c *gin.Context) {
		var input struct {
			Revision *int64          `json:"revision"`
			Template naming.Template `json:"template"`
		}
		if !bindNamingJSON(c, &input) {
			return
		}
		if input.Revision == nil || string(input.Template.Kind) != c.Param("kind") {
			namingError(c, naming.ErrInput)
			return
		}
		out, err := store.SaveNamingTemplate(c.Request.Context(), input.Template, *input.Revision)
		if err != nil {
			namingError(c, err)
			return
		}
		c.JSON(200, out)
	})
	group.POST("/preview", func(c *gin.Context) {
		var input struct {
			Template naming.Template `json:"template"`
			Items    []naming.Data   `json:"items"`
		}
		if !bindNamingJSON(c, &input) {
			return
		}
		if len(input.Items) == 0 || len(input.Items) > 50 {
			namingError(c, naming.ErrInput)
			return
		}
		renderer, err := naming.Compile(input.Template)
		if err != nil {
			namingError(c, err)
			return
		}
		type item struct {
			Result *naming.Result `json:"result,omitempty"`
			Error  string         `json:"error,omitempty"`
		}
		items := make([]item, 0, len(input.Items))
		for _, data := range input.Items {
			result, err := renderer.Render(data)
			if err != nil {
				items = append(items, item{Error: err.Error()})
			} else {
				items = append(items, item{Result: &result})
			}
		}
		c.JSON(200, gin.H{"items": items})
	})
}

func bindNamingJSON(c *gin.Context, input any) bool {
	if err := decodeStrictJSON(c.Request.Body, input); err != nil {
		namingError(c, naming.ErrInput)
		return false
	}
	return true
}

func decodeStrictJSON(reader io.Reader, input any) error {
	decoder := json.NewDecoder(reader)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(input); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		if err != nil {
			return err
		}
		return errors.New("请求只能包含一份 JSON")
	}
	return nil
}

func namingError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, storage.ErrNamingConflict):
		c.JSON(409, gin.H{"error": err.Error(), "code": "REVISION_CONFLICT"})
	case errors.Is(err, naming.ErrTemplate), errors.Is(err, naming.ErrInput):
		c.JSON(400, gin.H{"error": err.Error(), "code": "INVALID_NAMING"})
	default:
		c.JSON(500, gin.H{"error": "命名模板服务暂时不可用，请重试"})
	}
}
