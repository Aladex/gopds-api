package opds

import (
	"github.com/gin-gonic/gin"
)

const openSearchXML = `<?xml version="1.0" encoding="UTF-8"?>
<OpenSearchDescription xmlns="http://a9.com/-/spec/opensearch/1.1/">
  <ShortName>Лепробиблиотека</ShortName>
  <Description>Поиск книг и авторов</Description>
  <InputEncoding>UTF-8</InputEncoding>
  <OutputEncoding>UTF-8</OutputEncoding>
  <Url type="application/atom+xml;profile=opds-catalog" template="/opds/search?searchTerms={searchTerms}"/>
</OpenSearchDescription>`

// OpenSearch returns the OpenSearch description document
func OpenSearch(c *gin.Context) {
	c.Data(200, "application/opensearchdescription+xml; charset=utf-8", []byte(openSearchXML))
}
