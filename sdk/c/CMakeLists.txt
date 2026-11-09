cmake_minimum_required(VERSION 3.16)

project(acnologia_c VERSION 0.2.0 LANGUAGES C)

if(CMAKE_SOURCE_DIR STREQUAL CMAKE_CURRENT_SOURCE_DIR)
  set(ACN_TOP_LEVEL ON)
else()
  set(ACN_TOP_LEVEL OFF)
endif()
option(ACN_BUILD_TESTS "Build the offline tests" ${ACN_TOP_LEVEL})
option(ACN_BUILD_EXAMPLES "Build the example program" ${ACN_TOP_LEVEL})
option(ACN_SANITIZE "Build with AddressSanitizer and UndefinedBehaviorSanitizer (GCC and Clang)" OFF)
option(ACN_WERROR "Treat warnings as errors" ON)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)
set(CMAKE_C_EXTENSIONS OFF)

find_package(CURL REQUIRED)

# cJSON is vendored and built with its own, quieter flags.
add_library(acnologia_cjson OBJECT third_party/cjson/cJSON.c)
set_target_properties(acnologia_cjson PROPERTIES POSITION_INDEPENDENT_CODE ON)
if(MSVC)
  target_compile_definitions(acnologia_cjson PRIVATE _CRT_SECURE_NO_WARNINGS)
else()
  target_compile_options(acnologia_cjson PRIVATE -Wno-deprecated-declarations)
endif()

add_library(acnologia
  src/client.c
  src/errors.c
  src/http.c
  src/util.c
  $<TARGET_OBJECTS:acnologia_cjson>)
add_library(acnologia::acnologia ALIAS acnologia)
target_include_directories(acnologia
  PUBLIC $<BUILD_INTERFACE:${CMAKE_CURRENT_SOURCE_DIR}/include> $<INSTALL_INTERFACE:include>
  PRIVATE src third_party/cjson)
target_link_libraries(acnologia PRIVATE CURL::libcurl)

function(acn_strict target)
  if(MSVC)
    target_compile_options(${target} PRIVATE /W4)
    target_compile_definitions(${target} PRIVATE _CRT_SECURE_NO_WARNINGS)
    if(ACN_WERROR)
      target_compile_options(${target} PRIVATE /WX)
    endif()
  else()
    target_compile_options(${target} PRIVATE -Wall -Wextra -pedantic -Wshadow -Wstrict-prototypes)
    if(ACN_WERROR)
      target_compile_options(${target} PRIVATE -Werror)
    endif()
  endif()
endfunction()

function(acn_sanitize target)
  if(ACN_SANITIZE AND NOT MSVC)
    target_compile_options(${target} PRIVATE -fsanitize=address,undefined -fno-omit-frame-pointer -fno-sanitize-recover=all)
    target_link_options(${target} PRIVATE -fsanitize=address,undefined)
  endif()
endfunction()

acn_strict(acnologia)
acn_sanitize(acnologia)
acn_sanitize(acnologia_cjson)

if(ACN_BUILD_TESTS)
  enable_testing()
  find_package(Threads REQUIRED)
  add_executable(acnologia_tests tests/test_client.c tests/fake_server.c)
  target_link_libraries(acnologia_tests PRIVATE acnologia Threads::Threads)
  if(WIN32)
    target_link_libraries(acnologia_tests PRIVATE ws2_32)
  endif()
  acn_strict(acnologia_tests)
  acn_sanitize(acnologia_tests)
  add_test(NAME acnologia_tests COMMAND acnologia_tests)
  # Keep the developer's ACNOLOGIA_* settings out of the tests.
  set_tests_properties(acnologia_tests PROPERTIES
    ENVIRONMENT "ACNOLOGIA_URL=;ACNOLOGIA_TOKEN=;ACNOLOGIA_ACCESS_PASSWORD=;ACNOLOGIA_ACCESS_URL=")
endif()

if(ACN_BUILD_EXAMPLES)
  add_executable(acnologia_quickstart examples/quickstart.c)
  target_link_libraries(acnologia_quickstart PRIVATE acnologia)
  acn_strict(acnologia_quickstart)
  acn_sanitize(acnologia_quickstart)
endif()

include(GNUInstallDirs)
install(TARGETS acnologia EXPORT acnologiaTargets
  ARCHIVE DESTINATION ${CMAKE_INSTALL_LIBDIR}
  LIBRARY DESTINATION ${CMAKE_INSTALL_LIBDIR}
  RUNTIME DESTINATION ${CMAKE_INSTALL_BINDIR})
install(DIRECTORY include/ DESTINATION ${CMAKE_INSTALL_INCLUDEDIR})
