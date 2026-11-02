# frozen_string_literal: true

require_relative "lib/acnologia"

Gem::Specification.new do |s|
  s.name = "acnologia"
  s.version = Acnologia::VERSION
  s.summary = "Client for Acnologia, a self-hosted API key pool with automatic rotation."
  s.description = "Lease, report and proxy API keys (SerpAPI, ElevenLabs, any API key service) through an Acnologia server. Uses only net/http and json."
  s.authors = ["Acnologia"]
  s.license = "MIT"
  s.homepage = "https://github.com/sidharthpunathil/acnologia"
  s.files = Dir["lib/**/*.rb"] + ["README.md"]
  s.require_paths = ["lib"]
  s.required_ruby_version = ">= 2.6"
  s.add_development_dependency "minitest", ">= 5"
end
