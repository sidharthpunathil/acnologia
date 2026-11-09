package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;

import java.util.ArrayList;
import java.util.List;

/**
 * A capability the app may call, from {@code GET /api/v1/capabilities}: one
 * call served by whichever provider can. Call it with
 * {@link Acnologia#capability(String, Object, CapabilityOptions)}.
 *
 * @param slug        the capability slug, used in {@code /proxy/{slug}}
 * @param name        the display name
 * @param description what it does
 * @param strategy    how a route is picked
 * @param params      the parameters it takes, as JSON
 * @param routes      the services that can serve it
 * @param url         the full URL to POST to
 */
public record Capability(String slug, String name, String description, String strategy, JsonNode params,
                         List<Route> routes, String url) {

    /**
     * @param service the service slug
     * @param mapped  true when the route maps the answer to a common shape
     */
    public record Route(String service, boolean mapped) {
    }

    static Capability fromJson(JsonNode n) {
        List<Route> routes = new ArrayList<>();
        for (JsonNode r : n.path("routes")) {
            routes.add(new Route(Json.text(r, "service"), r.path("mapped").asBoolean(false)));
        }
        JsonNode p = n.get("params");
        return new Capability(Json.text(n, "slug"), Json.text(n, "name"), Json.text(n, "description"),
                Json.text(n, "strategy"), p == null ? Acnologia.JSON.nullNode() : p, List.copyOf(routes),
                Json.text(n, "url"));
    }
}
