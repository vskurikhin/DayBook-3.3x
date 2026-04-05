package su.svn;

import io.quarkus.test.common.QuarkusTestResourceLifecycleManager;
import org.testcontainers.containers.PostgreSQLContainer;

import java.io.IOException;
import java.util.Map;

public class PostgreSQLTestResource implements QuarkusTestResourceLifecycleManager {
    static PostgreSQLContainer<?> db = new PostgreSQLContainer<>("postgres:18");

    @Override
    public Map<String, String> start() {
        db.start();
        var url = String.format("vertx-reactive:postgresql://%s:%d/%s?%s",
                db.getHost(),
                db.getMappedPort(5432),
                db.getDatabaseName(),
                "currentSchema=api"
        );
        System.setProperty("quarkus.datasource.reactive.url",
                String.format("postgresql://%s:%d/%s",
                        db.getHost(),
                        db.getMappedPort(5432),
                        db.getDatabaseName()));
        try {
            db.execInContainer("psql",
                    "-U", db.getUsername(),
                    "-d", db.getDatabaseName(),
                    "-c", "CREATE SCHEMA IF NOT EXISTS api;");
        } catch (IOException | InterruptedException e) {
            throw new RuntimeException(e);
        }
        var jdbcUrl = db.getJdbcUrl() + "&currentSchema=api,public";
        System.out.println("jdbcUrl = " + jdbcUrl);
        System.out.println("db.getJdbcUrl() = " + db.getJdbcUrl());
        System.out.println("db.getDatabaseName() = " + db.getDatabaseName());
        System.out.println("db.getExposedPorts() = " + db.getExposedPorts());
        System.out.println("db.getFirstMappedPort() = " + db.getFirstMappedPort());
        System.out.println("db.getUsername() = " + db.getUsername());
        System.out.println("url = " + url);

        // Pass container properties to Quarkus
        return Map.of(
                "quarkus.datasource.jdbc.url", jdbcUrl,
                "quarkus.datasource.reactive.url", url,
                "quarkus.datasource.username", db.getUsername(),
                "quarkus.datasource.password", db.getPassword()
        );
    }

    @Override
    public void stop() {
        db.stop();
    }
}