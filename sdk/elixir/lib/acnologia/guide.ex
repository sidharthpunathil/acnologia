defmodule Acnologia.Guide do
  @moduledoc """
  A service's usage guide, from `GET /api/v1/services/{slug}/guide`.
  Returned by `Acnologia.guide/2`.

    * `markdown`: the guide as Markdown
    * `generated`: true when the provider has no written guide and this one
      was assembled from its examples and offers
    * `guide`: the structured guide as decoded JSON (endpoints, examples,
      limits, errors, notes and so on)
  """

  defstruct markdown: "", generated: false, guide: nil

  @type t :: %__MODULE__{markdown: String.t(), generated: boolean(), guide: term()}

  @doc false
  @spec from_json(map()) :: t()
  def from_json(data) when is_map(data) do
    %__MODULE__{
      markdown: if(is_binary(data["markdown"]), do: data["markdown"], else: ""),
      generated: data["generated"] == true,
      guide: data["guide"]
    }
  end
end
