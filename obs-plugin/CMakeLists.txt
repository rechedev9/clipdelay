cmake_minimum_required(VERSION 3.28...3.30)

include("${CMAKE_CURRENT_SOURCE_DIR}/cmake/common/bootstrap.cmake" NO_POLICY_SCOPE)

project(${_name} VERSION ${_version})

option(ENABLE_FRONTEND_API "Use obs-frontend-api for UI functionality" OFF)
option(ENABLE_QT "Use Qt functionality" OFF)

include(compilerconfig)
include(defaults)
include(helpers)

add_library(${CMAKE_PROJECT_NAME} MODULE)

find_package(libobs REQUIRED)
target_link_libraries(${CMAKE_PROJECT_NAME} PRIVATE OBS::libobs)

if(ENABLE_FRONTEND_API)
  find_package(obs-frontend-api REQUIRED)
  target_link_libraries(${CMAKE_PROJECT_NAME} PRIVATE OBS::obs-frontend-api)
endif()

if(ENABLE_QT)
  find_package(Qt6 COMPONENTS Widgets Core)
  target_link_libraries(${CMAKE_PROJECT_NAME} PRIVATE Qt6::Core Qt6::Widgets)
  target_compile_options(
    ${CMAKE_PROJECT_NAME}
    PRIVATE $<$<C_COMPILER_ID:Clang,AppleClang>:-Wno-quoted-include-in-framework-header -Wno-comma>
  )
  set_target_properties(
    ${CMAKE_PROJECT_NAME}
    PROPERTIES AUTOMOC ON AUTOUIC ON AUTORCC ON
  )
endif()

target_sources(
  ${CMAKE_PROJECT_NAME}
  PRIVATE src/plugin-main.cpp src/engine.cpp src/engine.hpp src/dock.cpp src/dock.hpp
)

# The delay engine is the Go relay one folder up, shipped next to the plugin's data.
find_program(GO_EXECUTABLE go REQUIRED)
set(engine_root "${CMAKE_CURRENT_SOURCE_DIR}/..")
set(engine_exe "${CMAKE_CURRENT_BINARY_DIR}/clipdelay-engine.exe")
file(GLOB_RECURSE engine_sources CONFIGURE_DEPENDS "${engine_root}/cmd/*.go" "${engine_root}/internal/*.go")
add_custom_command(
  OUTPUT "${engine_exe}"
  COMMAND "${GO_EXECUTABLE}" build -trimpath "-ldflags=-s -w" -o "${engine_exe}" ./cmd/clipdelay
  WORKING_DIRECTORY "${engine_root}"
  DEPENDS ${engine_sources} "${engine_root}/go.mod" "${engine_root}/go.sum"
  COMMENT "Build clipdelay-engine.exe"
  VERBATIM
)
add_custom_target(clipdelay-engine DEPENDS "${engine_exe}")
add_dependencies(${CMAKE_PROJECT_NAME} clipdelay-engine)
target_add_resource(${CMAKE_PROJECT_NAME} "${engine_exe}")

set_target_properties_plugin(${CMAKE_PROJECT_NAME} PROPERTIES OUTPUT_NAME ${_name})
