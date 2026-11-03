defmodule Acnologia.Me do
  @moduledoc """
  What the app token may do, from `GET /api/v1/me`. Returned by `Acnologia.me/1`.

    * `app`: `%{id: id, name: name}`
    * `limits`: `%{daily_requests:, monthly_requests:, daily_budget:, monthly_budget:}`,
      each nil when there is no such limit
    * `usage`: `%{today_requests:, today_cost:, month_requests:, month_cost:, last_used_at:}`
    * `limit_hit`: `%{limit: name, reset_at: DateTime}` when a limit is reached, else nil
    * `services`: one map per service the token may use, with `slug`, `name`,
      `provider`, `category` and `available` (keys free now: 1 or 0 for an
      ordinary app, a count for a share's token)
    * `share`: nil for an ordinary app; for a share's token a map with `id`,
      `name`, `owner_name`, `owner_id`, `recipient`, `label`, `status`,
      `mode` and `ends_at`
    * `raw`: the decoded JSON, for fields this struct does not name
  """

  defstruct app: %{id: nil, name: ""},
            limits: %{},
            usage: %{},
            limit_hit: nil,
            services: [],
            share: nil,
            raw: %{}

  @type service :: %{
          slug: String.t(),
          name: String.t(),
          provider: String.t(),
          category: String.t(),
          available: integer()
        }

  @type t :: %__MODULE__{
          app: %{id: term(), name: String.t()},
          limits: %{atom() => number() | nil},
          usage: %{atom() => term()},
          limit_hit: %{limit: String.t(), reset_at: DateTime.t() | nil} | nil,
          services: [service()],
          share: map() | nil,
          raw: map()
        }

  import Acnologia.Error, only: [parse_time: 1]

  @doc false
  @spec from_json(map()) :: t()
  def from_json(data) when is_map(data) do
    app = map(data["app"])
    limits = map(data["limits"])
    usage = map(data["usage"])

    %__MODULE__{
      app: %{id: app["id"], name: app["name"] || ""},
      limits: %{
        daily_requests: limits["daily_requests"],
        monthly_requests: limits["monthly_requests"],
        daily_budget: limits["daily_budget"],
        monthly_budget: limits["monthly_budget"]
      },
      usage: %{
        today_requests: usage["today_requests"] || 0,
        today_cost: usage["today_cost"] || 0,
        month_requests: usage["month_requests"] || 0,
        month_cost: usage["month_cost"] || 0,
        last_used_at: parse_time(usage["last_used_at"])
      },
      limit_hit: limit_hit(data["limit_hit"] || usage["limit_hit"]),
      services:
        for s <- List.wrap(data["services"]), is_map(s) do
          %{
            slug: s["slug"] || "",
            name: s["name"] || "",
            provider: s["provider"] || "",
            category: s["category"] || "",
            available: s["available"] || 0
          }
        end,
      share: share(data["share"]),
      raw: data
    }
  end

  defp limit_hit(%{} = hit), do: %{limit: hit["limit"], reset_at: parse_time(hit["reset_at"])}
  defp limit_hit(_), do: nil

  defp share(%{} = s) do
    %{
      id: s["id"],
      name: s["name"],
      owner_name: s["owner_name"],
      owner_id: s["owner_id"],
      recipient: s["recipient"],
      label: s["label"],
      status: s["status"],
      mode: s["mode"],
      ends_at: parse_time(s["ends_at"])
    }
  end

  defp share(_), do: nil

  defp map(%{} = m), do: m
  defp map(_), do: %{}
end
