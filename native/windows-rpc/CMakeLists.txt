cmake_minimum_required(VERSION 3.25)
project(airc_windows_rpc C)

add_compile_definitions(_WIN32_WINNT=0x0A00 WINVER=0x0A00 UNICODE _UNICODE)

if(NOT WIN32 OR NOT MSVC)
  message(FATAL_ERROR "Windows RPC workers require the Windows SDK and MSVC")
endif()

find_program(MIDL_EXECUTABLE midl REQUIRED)
if(CMAKE_GENERATOR_PLATFORM STREQUAL "ARM64")
  set(MIDL_ENV arm64)
elseif(CMAKE_GENERATOR_PLATFORM STREQUAL "x64")
  set(MIDL_ENV x64)
else()
  message(FATAL_ERROR "Only Windows AMD64 and ARM64 RPC workers are supported")
endif()

set(GENERATED_DIR "${CMAKE_CURRENT_BINARY_DIR}/midl")
file(MAKE_DIRECTORY "${GENERATED_DIR}")
add_custom_command(
  OUTPUT "${GENERATED_DIR}/gateway.h" "${GENERATED_DIR}/gateway_c.c" "${GENERATED_DIR}/gateway_s.c"
  COMMAND "${MIDL_EXECUTABLE}" /nologo /robust /Oicf /target NT60 /env ${MIDL_ENV}
          /h gateway.h /client stub /server stub
          /out "${GENERATED_DIR}" "${CMAKE_CURRENT_SOURCE_DIR}/gateway.idl"
  DEPENDS "${CMAKE_CURRENT_SOURCE_DIR}/gateway.idl"
  VERBATIM)

add_custom_target(gateway_midl DEPENDS "${GENERATED_DIR}/gateway.h" "${GENERATED_DIR}/gateway_c.c" "${GENERATED_DIR}/gateway_s.c")

add_library(gateway_client_stub OBJECT "${GENERATED_DIR}/gateway_c.c")
add_library(gateway_server_stub OBJECT "${GENERATED_DIR}/gateway_s.c")
add_dependencies(gateway_client_stub gateway_midl)
add_dependencies(gateway_server_stub gateway_midl)
target_include_directories(gateway_client_stub PRIVATE "${GENERATED_DIR}")
target_include_directories(gateway_server_stub PRIVATE "${GENERATED_DIR}")

add_executable(airc-rpc-client client.c ../windows-worker/common.c allocator.c $<TARGET_OBJECTS:gateway_client_stub>)
add_executable(airc-rpc-server server.c ../windows-worker/common.c allocator.c $<TARGET_OBJECTS:gateway_server_stub>)
add_dependencies(airc-rpc-client gateway_client_stub)
add_dependencies(airc-rpc-server gateway_server_stub)
foreach(target airc-rpc-client airc-rpc-server)
  target_include_directories(${target} PRIVATE "${GENERATED_DIR}")
  target_compile_features(${target} PRIVATE c_std_11)
  target_compile_options(${target} PRIVATE /W4 /WX /sdl)
  target_link_libraries(${target} PRIVATE Rpcrt4 Advapi32)
endforeach()

set_target_properties(airc-rpc-client airc-rpc-server PROPERTIES C_STANDARD 11 C_STANDARD_REQUIRED YES)
