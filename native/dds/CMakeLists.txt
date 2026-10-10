cmake_minimum_required(VERSION 3.20)
project(airc_dds_bundle C)

if(NOT CMAKE_SYSTEM_NAME STREQUAL "Linux")
  message(FATAL_ERROR "The bounded DDS helper requires Linux")
endif()

find_package(CycloneDDS 0.10.5 EXACT REQUIRED)

idlc_generate(TARGET airc_dds_idl FILES AIRC.idl)

add_executable(airc-dds main.c)
target_compile_features(airc-dds PRIVATE c_std_11)
target_compile_options(airc-dds PRIVATE -Wall -Wextra -Werror -pedantic)
target_link_libraries(airc-dds PRIVATE CycloneDDS::ddsc airc_dds_idl)
