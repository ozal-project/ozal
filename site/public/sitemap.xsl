<?xml version="1.0" encoding="UTF-8"?>
<xsl:stylesheet version="1.0"
  xmlns:xsl="http://www.w3.org/1999/XSL/Transform"
  xmlns:sm="http://www.sitemaps.org/schemas/sitemap/0.9"
  exclude-result-prefixes="sm">
  <xsl:output method="html" encoding="UTF-8" indent="yes" />

  <xsl:template match="/">
    <html lang="en">
      <head>
        <meta charset="utf-8" />
        <meta name="viewport" content="width=device-width, initial-scale=1" />
        <title>Sitemap</title>
        <link rel="stylesheet" href="/sitemap.css" />
      </head>
      <body>
        <main>
          <p class="eyebrow">Sitemap</p>
          <h1>
            <xsl:choose>
              <xsl:when test="sm:sitemapindex">Sitemap index</xsl:when>
              <xsl:otherwise>Sitemap</xsl:otherwise>
            </xsl:choose>
          </h1>
          <p class="lede">This is the machine-readable sitemap. Search engines read the XML directly; this table is only for people.</p>
          <table>
            <thead>
              <tr>
                <th scope="col">URL</th>
                <th scope="col">Last modified</th>
                <th scope="col">Change frequency</th>
                <th scope="col">Priority</th>
              </tr>
            </thead>
            <tbody>
              <xsl:for-each select="sm:urlset/sm:url | sm:sitemapindex/sm:sitemap">
                <tr>
                  <td><a href="{sm:loc}"><xsl:value-of select="sm:loc" /></a></td>
                  <td><xsl:value-of select="sm:lastmod" /></td>
                  <td><xsl:value-of select="sm:changefreq" /></td>
                  <td><xsl:value-of select="sm:priority" /></td>
                </tr>
              </xsl:for-each>
            </tbody>
          </table>
          <p class="foot"><a href="/">Home</a></p>
        </main>
      </body>
    </html>
  </xsl:template>
</xsl:stylesheet>
