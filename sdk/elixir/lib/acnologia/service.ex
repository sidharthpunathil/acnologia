defmodule Acnologia.Service do
  @moduledoc """
  A service the token may use, from `GET /api/v1/services`. Returned by
  `Acnologia.services/1`.

    * `slug`, `name`, `provider`, `category`
    * `available`: whether a key can be handed out right now
    * `base_url`: the provider's base URL
    * `auth`: `%{type: "query" | "header" | "bearer", name: name, prefix: prefix}`
    * `guide_url`: where the service's usage guide is; read it with
      `Acnologia.guide/2` or `Acnologia.guide_markdown/2`
  """

  defstruct slug: "",
            name: "",
            provider: "",
            category: "",
            available: false,
            base_url: "",
            auth: %{type: "header", name: "", prefix: ""},
            guide_url: ""

  @type t :: %__MODULE__{
          slug: String.t(),
          name: String.t(),
          provider: String.t(),
          category: String.t(),
          available: boolean(),
          base_url: String.t(),
          auth: %{type: String.t(), name: String.t(), prefix: String.t()},
          guide_url: String.t()
        }

  @doc false
  @spec from_json(map()) :: t()
  def from_json(data) when is_map(data) do
    auth = if is_map(data["auth"]), do: data["auth"], else: %{}
    slug = str(data["slug"])

    %__MODULE__{
      slug: slug,
      name: if(str(data["name"]) == "", do: slug, else: data["name"]),
      provider: str(data["provider"]),
      category: str(data["category"]),
      available: data["available"] in [true, 1],
      base_url: str(data["base_url"]),
      auth: %{
        type: if(str(auth["type"]) == "", do: "header", else: auth["type"]),
        name: str(auth["name"]),
        prefix: str(auth["prefix"])
      },
      guide_url: str(data["guide_url"])
    }
  end

  defp str(v) when is_binary(v), do: v
  defp str(_), do: ""
end
