package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;

import java.time.Instant;

/** Small readers for optional JSON fields. */
final class Json {
    private Json() {
    }

    static String text(JsonNode n, String field) {
        return n.path(field).asText("");
    }

    static String textOrNull(JsonNode n, String field) {
        JsonNode v = n.get(field);
        return v == null || v.isNull() ? null : v.asText();
    }

    static Long longOrNull(JsonNode n, String field) {
        JsonNode v = n.get(field);
        return v == null || v.isNull() ? null : v.asLong();
    }

    static Double doubleOrNull(JsonNode n, String field) {
        JsonNode v = n.get(field);
        return v == null || v.isNull() ? null : v.asDouble();
    }

    static Instant time(JsonNode n, String field) {
        return Lease.parseTime(n.path(field).asText(""));
    }
}
