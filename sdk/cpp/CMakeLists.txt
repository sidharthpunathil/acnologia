cmake_minimum_required(VERSION 3.16)

project(acnologia VERSION 0.1.0 DESCRIPTION "C++ client for Acnologia, a self-hosted API key pool" LANGUAGES CXX)

if(CMAKE_SOURCE_DIR STREQUAL PROJECT_SOURCE_DIR)
  set(ACNOLOGIA_TOP_LEVEL ON)
else()
  set(ACNOLOGIA_TOP_LEVEL OFF)
endif()

option(ACNOLOGIA_BUILD_TESTS "Build the acnologia tests" ${ACNOLOGIA_TOP_LEVEL})
option(ACNOLOGIA_INSTALL "Generate install and export rules" ${ACNOLOGIA_TOP_LEVEL})

# Extracted files get the extraction time, so a changed URL triggers a rebuild.
if(POLICY CMP0135)
  cmake_policy(SET CMP0135 NEW)
endif()

include(FetchContent)
include(GNUInstallDirs)

find_package(CURL REQUIRED)

# nlohmann/json is used only inside src/, never in the public headers.
if(NOT TARGET nlohmann_json::nlohmann_json)
  set(JSON_Install OFF CACHE INTERNAL "")
  FetchContent_Declare(acnologia_nlohmann_json
    URL https://github.com/nlohmann/json/releases/download/v3.12.0/json.tar.xz
    URL_HASH SHA256=42f6e95cad6ec532fd372391373363b62a14af6d771056dbfc86160e6dfff7aa)
  FetchContent_MakeAvailable(acnologia_nlohmann_json)
endif()

add_library(acnologia
  src/client.cpp
  src/curl_transport.cpp
  src/errors.cpp
  src/types.cpp)
add_library(acnologia::acnologia ALIAS acnologia)

target_compile_features(acnologia PUBLIC cxx_std_17)
set_target_properties(acnologia PROPERTIES CXX_EXTENSIONS OFF)
target_include_directories(acnologia
  PUBLIC
    $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include>
    $<INSTALL_INTERFACE:${CMAKE_INSTALL_INCLUDEDIR}>
  PRIVATE
    ${CMAKE_CURRENT_SOURCE_DIR}/src)
target_link_libraries(acnologia
  PRIVATE
    CURL::libcurl
    $<BUILD_INTERFACE:nlohmann_json::nlohmann_json>)

if(MSVC)
  target_compile_options(acnologia PRIVATE /W4)
else()
  target_compile_options(acnologia PRIVATE -Wall -Wextra -Wpedantic)
endif()

if(ACNOLOGIA_INSTALL)
  include(CMakePackageConfigHelpers)
  install(TARGETS acnologia EXPORT acnologiaTargets
    ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
    LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
    RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR})
  install(DIRECTORY include/acnologia DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
  install(EXPORT acnologiaTargets NAMESPACE acnologia:: DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/acnologia)
  configure_package_config_file(cmake/acnologiaConfig.cmake.in
    ${CMAKE_CURRENT_BINARY_DIR}/acnologiaConfig.cmake
    INSTALL_DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/acnologia)
  write_basic_package_version_file(${CMAKE_CURRENT_BINARY_DIR}/acnologiaConfigVersion.cmake
    COMPATIBILITY SameMinorVersion)
  install(FILES
    ${CMAKE_CURRENT_BINARY_DIR}/acnologiaConfig.cmake
    ${CMAKE_CURRENT_BINARY_DIR}/acnologiaConfigVersion.cmake
    DESTINATION ${CMAKE_INSTALL_LIBDIR}/cmake/acnologia)
endif()

if(ACNOLOGIA_BUILD_TESTS)
  enable_testing()
  add_subdirectory(tests)
endif()
