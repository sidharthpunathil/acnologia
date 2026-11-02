plugins {
    kotlin("jvm") version "2.0.21"
    kotlin("plugin.serialization") version "2.0.21"
    `java-library`
    `maven-publish`
}

group = "dev.acnologia"
version = "0.1.0"

repositories {
    mavenCentral()
}

dependencies {
    api("com.squareup.okhttp3:okhttp:4.12.0")
    api("org.jetbrains.kotlinx:kotlinx-coroutines-core:1.9.0")
    api("org.jetbrains.kotlinx:kotlinx-serialization-json:1.7.3")

    testImplementation(kotlin("test"))
    testImplementation("com.squareup.okhttp3:mockwebserver:4.12.0")
}

kotlin {
    jvmToolchain(17)
    explicitApi()
}

java {
    withSourcesJar()
}

tasks.test {
    useJUnitPlatform()
}

publishing {
    publications {
        create<MavenPublication>("maven") {
            artifactId = "acnologia-kotlin"
            from(components["java"])
            pom {
                name.set("Acnologia Kotlin SDK")
                description.set("Kotlin coroutine client for Acnologia, a self-hosted API key pool.")
            }
        }
    }
}
