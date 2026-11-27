FROM maven:3.10-eclipse-temurin-25 AS build
WORKDIR /src
COPY pom.xml ./
RUN mvn -q -B dependency:go-offline
COPY src ./src
RUN mvn -q -B -o package

# distroless/java25: a JRE and CA certificates, no shell.
FROM gcr.io/distroless/java25-debian13:nonroot
WORKDIR /app
COPY --from=build /src/target/bench.jar ./
COPY --from=build /src/target/lib ./lib
USER 65532:65532
ENTRYPOINT ["java", "-jar", "/app/bench.jar"]
