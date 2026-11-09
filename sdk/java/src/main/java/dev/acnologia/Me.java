package dev.acnologia;

import com.fasterxml.jackson.databind.JsonNode;

import java.time.Instant;
import java.util.ArrayList;
import java.util.List;

/**
 * What an app token may do, from {@code GET /api/v1/me}: the app, its limits
 * and use so far, the services it may call and, for a share's token, the
 * share. A share token whose share stopped still gets an answer, with the
 * share's {@code status} and no services.
 *
 * @param app      the app
 * @param limits   the app's request and budget limits, each null when unset
 * @param usage    requests and cost so far
 * @param limitHit the limit the app has reached, or null
 * @param services the services the token may call
 * @param share    the share behind the token, or null for an ordinary app
 */
public record Me(App app, Limits limits, Usage usage, LimitHit limitHit, List<MeService> services, Share share) {

    /**
     * @param id   the app id
     * @param name the app name (the share's name for a share's token)
     */
    public record App(long id, String name) {
    }

    /**
     * Limits, each null when the app has none.
     *
     * @param dailyRequests   requests per day
     * @param monthlyRequests requests per month
     * @param dailyBudget     cost per day
     * @param monthlyBudget   cost per month
     */
    public record Limits(Long dailyRequests, Long monthlyRequests, Double dailyBudget, Double monthlyBudget) {
    }

    /**
     * @param todayRequests requests today
     * @param todayCost     cost today
     * @param monthRequests requests this month
     * @param monthCost     cost this month
     * @param lastUsedAt    the last request, or null
     * @param limitHit      the limit reached, or null
     */
    public record Usage(long todayRequests, double todayCost, long monthRequests, double monthCost,
                        Instant lastUsedAt, LimitHit limitHit) {
    }

    /**
     * @param limit   {@code daily_requests}, {@code monthly_requests}, {@code daily_budget} or {@code monthly_budget}
     * @param resetAt when the limit resets, or null
     */
    public record LimitHit(String limit, Instant resetAt) {
    }

    /**
     * @param slug      the service slug
     * @param name      the display name
     * @param provider  the provider slug
     * @param category  the category
     * @param available keys free right now for a share's token; 1 or 0 for an ordinary app
     * @param logoUrl   the logo as a {@code data:} URI, or null
     */
    public record MeService(String slug, String name, String provider, String category, int available,
                            String logoUrl) {
    }

    /**
     * @param id        the share id
     * @param name      the share name
     * @param ownerName who shared it
     * @param recipient who it was shared with
     * @param label     the owner's label
     * @param status    {@code active}, {@code paused}, {@code expired} or {@code revoked}
     * @param mode      the share's mode
     * @param endsAt    when the share ends, or null
     */
    public record Share(String id, String name, String ownerName, String recipient, String label, String status,
                        String mode, Instant endsAt) {
    }

    static Me fromJson(JsonNode n) {
        JsonNode a = n.path("app");
        JsonNode l = n.path("limits");
        JsonNode u = n.path("usage");
        List<MeService> svcs = new ArrayList<>();
        for (JsonNode s : n.path("services")) {
            svcs.add(new MeService(Json.text(s, "slug"), Json.text(s, "name"), Json.text(s, "provider"),
                    Json.text(s, "category"), s.path("available").asInt(0), Json.textOrNull(s, "logo_url")));
        }
        JsonNode sh = n.get("share");
        Share share = sh == null || !sh.isObject() ? null
                : new Share(Json.text(sh, "id"), Json.text(sh, "name"), Json.text(sh, "owner_name"),
                        Json.text(sh, "recipient"), Json.text(sh, "label"), Json.text(sh, "status"),
                        Json.text(sh, "mode"), Json.time(sh, "ends_at"));
        return new Me(new App(a.path("id").asLong(0), Json.text(a, "name")),
                new Limits(Json.longOrNull(l, "daily_requests"), Json.longOrNull(l, "monthly_requests"),
                        Json.doubleOrNull(l, "daily_budget"), Json.doubleOrNull(l, "monthly_budget")),
                new Usage(u.path("today_requests").asLong(0), u.path("today_cost").asDouble(0),
                        u.path("month_requests").asLong(0), u.path("month_cost").asDouble(0),
                        Json.time(u, "last_used_at"), limitHit(u.get("limit_hit"))),
                limitHit(n.get("limit_hit")), List.copyOf(svcs), share);
    }

    private static LimitHit limitHit(JsonNode n) {
        if (n == null || !n.isObject()) {
            return null;
        }
        return new LimitHit(Json.text(n, "limit"), Json.time(n, "reset_at"));
    }
}
