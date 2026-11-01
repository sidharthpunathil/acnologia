# frozen_string_literal: true

require_relative "lib/cogito"

Gem::Specification.new do |s|
  s.name = "cogito-keys"
  s.version = Cogito::VERSION
  s.summary = "Client for Cogito, a self-hosted API key pool with automatic rotation."
  s.description = "Lease, report and proxy API keys (SerpAPI, ElevenLabs, any API key service) through a Cogito server. Uses only net/http and json."
  s.authors = ["Cogito"]
  s.license = "MIT"
  s.homepage = "https://github.com/cogito-keys/cogito"
  s.files = Dir["lib/**/*.rb"] + ["README.md"]
  s.require_paths = ["lib"]
  s.required_ruby_version = ">= 2.6"
  s.add_development_dependency "minitest", ">= 5"
end
