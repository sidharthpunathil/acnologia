FetchContent_Declare(acnologia_doctest
  URL https://github.com/doctest/doctest/archive/refs/tags/v2.4.12.tar.gz
  URL_HASH SHA256=73381c7aa4dee704bd935609668cf41880ea7f19fa0504a200e13b74999c2d70
  # Header only: a SOURCE_SUBDIR without a CMakeLists.txt skips doctest's own build.
  SOURCE_SUBDIR doctest)
FetchContent_MakeAvailable(acnologia_doctest)

add_executable(acnologia_tests test_client.cpp)
target_link_libraries(acnologia_tests PRIVATE acnologia::acnologia nlohmann_json::nlohmann_json)
target_include_directories(acnologia_tests PRIVATE ${acnologia_doctest_SOURCE_DIR})
add_test(NAME acnologia_tests COMMAND acnologia_tests)

if(UNIX)
  find_package(Threads REQUIRED)
  add_executable(acnologia_integration_tests test_integration.cpp)
  target_link_libraries(acnologia_integration_tests PRIVATE acnologia::acnologia Threads::Threads)
  target_include_directories(acnologia_integration_tests PRIVATE ${acnologia_doctest_SOURCE_DIR})
  add_test(NAME acnologia_integration_tests COMMAND acnologia_integration_tests)
endif()
