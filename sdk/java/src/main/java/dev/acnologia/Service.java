package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;

/** A service this app token may use, from {@code GET /api/v1/services}. */
public final class Service {
    private final String slug;
    private final String name;
    private final String provider;
    private final String category;
    private final boolean available;
    private final String baseUrl;
    private final Auth auth;

    Service(String slug, String name, String provider, String category, boolean available, String baseUrl, Auth auth) {
        this.slug = slug;
        this.name = name;
        this.provider = provider;
        this.category = category;
        this.available = available;
        this.baseUrl = baseUrl;
        this.auth = auth;
    }

    static Service fromJson(JsonNode n) {
        return new Service(n.path("slug").asText(""), n.path("name").asText(""), n.path("provider").asText(""),
                n.path("category").asText(""), n.path("available").asBoolean(false), n.path("base_url").asText(""),
                Auth.fromJson(n.get("auth")));
    }

    /** @return the service slug used in leases and proxy paths */
    public String slug() {
        return slug;
    }

    /** @return the display name */
    public String name() {
        return name;
    }

    /** @return the provider slug */
    public String provider() {
        return provider;
    }

    /** @return the category, such as {@code search} */
    public String category() {
        return category;
    }

    /** @return true when a key is free to lease right now */
    public boolean available() {
        return available;
    }

    /** @return the provider base URL */
    public String baseUrl() {
        return baseUrl;
    }

    /** @return where the provider expects the key */
    public Auth auth() {
        return auth;
    }

    @Override
    public String toString() {
        return "Service{slug=" + slug + ", name=" + name + ", provider=" + provider + ", category=" + category
                + ", available=" + available + ", baseUrl=" + baseUrl + ", auth=" + auth + "}";
    }
}
