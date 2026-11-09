defmodule Acnologia.Capability do
  @moduledoc """
  A capability from `GET /api/v1/capabilities`: one job, such as finding
  places, that several providers do. Returned by `Acnologia.capabilities/1`
  and called with `Acnologia.capability/4`.

    * `slug`, `name`, `description`
    * `strategy`: how a route is picked, for example `"priority"`
    * `params`: the params it takes, as decoded JSON (a list of maps with
      `name`, `type`, `required`, `description` and `default`)
    * `routes`: `[%{service: slug, mapped: boolean}]`, the providers it can use
    * `url`: the full URL to call
  """

  defstruct slug: "", name: "", description: "", strategy: "", params: nil, routes: [], url: ""

  @type t :: %__MODULE__{
          slug: String.t(),
          name: String.t(),
          description: String.t(),
          strategy: String.t(),
          params: term(),
          routes: [%{service: String.t(), mapped: boolean()}],
          url: String.t()
        }

  @doc false
  @spec from_json(map()) :: t()
  def from_json(data) when is_map(data) do
    slug = str(data["slug"])

    %__MODULE__{
      slug: slug,
      name: if(str(data["name"]) == "", do: slug, else: data["name"]),
      description: str(data["description"]),
      strategy: str(data["strategy"]),
      params: data["params"],
      routes:
        for r <- List.wrap(data["routes"]), is_map(r) do
          %{service: str(r["service"]), mapped: r["mapped"] == true}
        end,
      url: str(data["url"])
    }
  end

  defp str(v) when is_binary(v), do: v
  defp str(_), do: ""
end
