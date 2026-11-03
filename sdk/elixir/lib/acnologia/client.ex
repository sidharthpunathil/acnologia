defmodule Acnologia.Client do
  @moduledoc """
  Connection settings for one Acnologia server. Build one with `Acnologia.new/1`.

  `inspect/1` never shows the token or the access password.
  """

  @derive {Inspect, except: [:token, :access_password]}
  @enforce_keys [:base_url, :token]
  defstruct base_url: nil,
            token: nil,
            access_password: nil,
            timeout: 30_000,
            connect_timeout: 10_000,
            ssl: nil

  @type t :: %__MODULE__{
          base_url: String.t(),
          token: String.t(),
          access_password: String.t() | nil,
          timeout: pos_integer(),
          connect_timeout: pos_integer(),
          ssl: keyword() | nil
        }

  @doc false
  @spec secrets(t()) :: [String.t() | nil]
  def secrets(%__MODULE__{token: t, access_password: a}), do: [t, a]
end
