# frozen_string_literal: true

require_relative "lib/acnologia"

Gem::Specification.new do |s|
  s.name = "acnologia"
  s.version = Acnologia::VERSION
  s.summary = "Client for Acnologia, a self-hosted API key pool with automatic rotation."
  s.description = "Proxy requests through an Acnologia server, or lease, report and renew its API keys. " \
                  "Uses only the Ruby standard library."
  s.authors = ["Acnologia"]
  s.license = "MIT"
  s.homepage = "https://github.com/sidharthpunathil/acnologia"
  s.files = Dir["lib/**/*.rb"] + ["README.md"]
  s.require_paths = ["lib"]
  s.required_ruby_version = ">= 3.1"
  s.add_development_dependency "minitest", ">= 5"
end
