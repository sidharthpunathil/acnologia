package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;

/**
 * A service's usage guide, from {@code GET /api/v1/services/{slug}/guide}.
 *
 * @param markdown  the guide as Markdown
 * @param generated true when the server wrote the guide from the provider spec
 * @param guide     the structured guide, as JSON
 */
public record Guide(String markdown, boolean generated, JsonNode guide) {
    static Guide fromJson(JsonNode n) {
        JsonNode g = n.get("guide");
        return new Guide(Json.text(n, "markdown"), n.path("generated").asBoolean(false),
                g == null ? Acnologia.JSON.nullNode() : g);
    }
}
