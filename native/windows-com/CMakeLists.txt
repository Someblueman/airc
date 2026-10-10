cmake_minimum_required(VERSION 3.25)
project(airc_windows_com C)

add_compile_definitions(_WIN32_WINNT=0x0A00 WINVER=0x0A00 UNICODE _UNICODE CONST_VTABLE)

if(NOT WIN32 OR NOT MSVC)
  message(FATAL_ERROR "Windows COM worker requires the Windows SDK and MSVC")
endif()

if(NOT CMAKE_GENERATOR_PLATFORM MATCHES "^(x64|ARM64)$")
  message(FATAL_ERROR "Only Windows AMD64 and ARM64 COM workers are supported")
endif()

add_executable(airc-com-client client.c ../windows-worker/common.c)
add_executable(airc-com-server server.c dispatch.c ../windows-worker/common.c)
foreach(target airc-com-client airc-com-server)
  target_include_directories(${target} PRIVATE "${CMAKE_CURRENT_SOURCE_DIR}")
  target_compile_features(${target} PRIVATE c_std_11)
  target_compile_options(${target} PRIVATE /W4 /WX /sdl)
  target_link_libraries(${target} PRIVATE Ole32 OleAut32 Rpcrt4 Advapi32 Uuid)
  set_target_properties(${target} PROPERTIES C_STANDARD 11 C_STANDARD_REQUIRED YES)
endforeach()
