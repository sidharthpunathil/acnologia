defmodule Acnologia.MixProject do
  use Mix.Project

  @version "0.2.0"

  def project do
    [
      app: :acnologia,
      version: @version,
      elixir: "~> 1.18",
      elixirc_paths: elixirc_paths(Mix.env()),
      start_permanent: Mix.env() == :prod,
      deps: [],
      description: "Elixir client for Acnologia, a self-hosted API key server.",
      source_url: "https://github.com/sidharthpunathil/acnologia"
    ]
  end

  def application do
    [extra_applications: [:logger, :inets, :ssl, :public_key, :crypto]]
  end

  defp elixirc_paths(:test), do: ["lib", "test/support"]
  defp elixirc_paths(_), do: ["lib"]
end
