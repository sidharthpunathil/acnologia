const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    // The library. Other projects import it as `acnologia`.
    const mod = b.addModule("acnologia", .{
        .root_source_file = b.path("src/acnologia.zig"),
        .target = target,
        .optimize = optimize,
    });

    // Unit tests inside the library sources.
    const unit_tests = b.addTest(.{ .root_module = mod });

    // Client tests against a fake server started inside the test.
    const client_tests = b.addTest(.{
        .root_module = b.createModule(.{
            .root_source_file = b.path("tests/client_test.zig"),
            .target = target,
            .optimize = optimize,
            .imports = &.{.{ .name = "acnologia", .module = mod }},
        }),
    });

    const test_step = b.step("test", "Run the unit and client tests");
    test_step.dependOn(&b.addRunArtifact(unit_tests).step);
    test_step.dependOn(&b.addRunArtifact(client_tests).step);

    // A small program that calls a real server, configured from the
    // environment: `zig build example`.
    const example = b.addExecutable(.{
        .name = "acnologia-example",
        .root_module = b.createModule(.{
            .root_source_file = b.path("examples/main.zig"),
            .target = target,
            .optimize = optimize,
            .imports = &.{.{ .name = "acnologia", .module = mod }},
        }),
    });
    const run_example = b.addRunArtifact(example);
    run_example.addPassthruArgs();
    b.step("example", "Run examples/main.zig against ACNOLOGIA_URL").dependOn(&run_example.step);
}
