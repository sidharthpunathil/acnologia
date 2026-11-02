package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;

import java.util.Objects;

/**
 * Where a provider expects the key. {@code type} is {@code query},
 * {@code header} or {@code bearer}; {@code name} is the query parameter or
 * header name. {@code prefix}, when not empty, goes in front of the secret for
 * every type (for bearer it replaces the default {@code "Bearer "}).
 */
public final class Auth {
    private final String type;
    private final String name;
    private final String prefix;

    /**
     * @param type   {@code query}, {@code header} or {@code bearer}
     * @param name   the parameter or header name
     * @param prefix text put in front of the secret, may be empty
     */
    public Auth(String type, String name, String prefix) {
        this.type = type == null || type.isEmpty() ? "header" : type;
        this.name = name == null ? "" : name;
        this.prefix = prefix == null ? "" : prefix;
    }

    static Auth fromJson(JsonNode n) {
        if (n == null || !n.isObject()) {
            return new Auth("header", "", "");
        }
        return new Auth(n.path("type").asText(""), n.path("name").asText(""), n.path("prefix").asText(""));
    }

    /** @return {@code query}, {@code header} or {@code bearer} */
    public String type() {
        return type;
    }

    /** @return the parameter or header name, may be empty */
    public String name() {
        return name;
    }

    /** @return the prefix put in front of the secret, may be empty */
    public String prefix() {
        return prefix;
    }

    @Override
    public boolean equals(Object o) {
        if (!(o instanceof Auth)) {
            return false;
        }
        Auth a = (Auth) o;
        return type.equals(a.type) && name.equals(a.name) && prefix.equals(a.prefix);
    }

    @Override
    public int hashCode() {
        return Objects.hash(type, name, prefix);
    }

    @Override
    public String toString() {
        return "Auth{type=" + type + ", name=" + name + ", prefix=" + prefix + "}";
    }
}
