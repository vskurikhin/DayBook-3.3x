package su.svn.api.resources;

import io.quarkus.test.common.QuarkusTestResourceLifecycleManager;
import org.testcontainers.containers.PostgreSQLContainer;

import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.net.InetAddress;
import java.net.ServerSocket;
import java.net.Socket;
import java.net.SocketException;
import java.nio.charset.StandardCharsets;
import java.util.HashMap;
import java.util.Locale;
import java.util.Map;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.TimeUnit;

/** Disposable PostgreSQL and a Core HTTP endpoint that deliberately never responds. */
public class PageReadIntegrationResource implements QuarkusTestResourceLifecycleManager {
    private static final String REACTIVE_URL_PROPERTY = "quarkus.datasource.reactive.url";
    private String previousReactiveUrl;
    private boolean reactiveUrlOverridden;
    private PostgreSQLContainer<?> database;
    private ServerSocket server;
    private volatile Socket connection;
    private ExecutorService worker;
    private final CoreRequest core = new CoreRequest();

    public static final class CoreRequest {
        final CompletableFuture<Map<String, String>> received = new CompletableFuture<>();
        final CompletableFuture<Void> closed = new CompletableFuture<>();
    }

    @Override
    public Map<String, String> start() {
        try {
            database = new PostgreSQLContainer<>("pgvector/pgvector:pg18");
            database.start();
            try (var connection = database.createConnection(""); var statement = connection.createStatement()) {
                statement.execute("CREATE SCHEMA IF NOT EXISTS api");
            }
            server = new ServerSocket(0, 1, InetAddress.getByName("127.0.0.1"));
            server.setSoTimeout(10000);
            worker = Executors.newSingleThreadExecutor();
            worker.submit(this::receiveCoreRequest);
            var reactiveUrl = "postgresql://" + database.getHost() + ":"
                    + database.getMappedPort(5432) + "/" + database.getDatabaseName();
            // Earlier profiles leave a system property, whose priority exceeds resource config overrides.
            previousReactiveUrl = System.getProperty(REACTIVE_URL_PROPERTY);
            System.setProperty(REACTIVE_URL_PROPERTY, reactiveUrl);
            reactiveUrlOverridden = true;
            return Map.of(
                    "quarkus.datasource.jdbc.url", database.getJdbcUrl() + "&currentSchema=api,public",
                    REACTIVE_URL_PROPERTY, reactiveUrl,
                    "quarkus.datasource.username", database.getUsername(),
                    "quarkus.datasource.password", database.getPassword(),
                    "quarkus.rest-client.\"su.svn.api.repository.client.rest.RecordViewClient\".url",
                    "http://127.0.0.1:" + server.getLocalPort(),
                    "quarkus.rest-client.\"su.svn.api.repository.client.rest.RecordViewClient\".proxy-address", "none",
                    "application.records-page.remote-timeout-ms", "60000",
                    "quarkus.scheduler.enabled", "false");
        } catch (Exception failure) {
            stop();
            throw new IllegalStateException("Cannot start isolated page-read fixtures", failure);
        }
    }

    @Override
    public void inject(TestInjector injector) {
        injector.injectIntoFields(core, new TestInjector.MatchesType(CoreRequest.class));
    }

    private void receiveCoreRequest() {
        try (var socket = server.accept()) {
            connection = socket;
            socket.setSoTimeout(10000);
            var input = new BufferedReader(new InputStreamReader(socket.getInputStream(), StandardCharsets.US_ASCII));
            var requestLine = input.readLine();
            var headers = new HashMap<String, String>();
            for (String line; (line = input.readLine()) != null && !line.isEmpty();) {
                int colon = line.indexOf(':');
                if (colon > 0) headers.put(line.substring(0, colon).toLowerCase(Locale.ROOT), line.substring(colon + 1).trim());
            }
            core.received.complete(Map.of(
                    "path", requestLine.split(" ")[1].split("\\?")[0],
                    "authorization", headers.getOrDefault("authorization", ""),
                    "requestId", headers.getOrDefault("x-request-id", "")));
            // GET has no body. Withhold the response and observe actual client disconnect.
            if (input.read() != -1) throw new IllegalStateException("Unexpected Core request body");
            core.closed.complete(null);
        } catch (SocketException disconnected) {
            if (core.received.isDone()) core.closed.complete(null);
            else core.received.completeExceptionally(disconnected);
        } catch (Exception failure) {
            core.received.completeExceptionally(failure);
            core.closed.completeExceptionally(failure);
        }
    }

    @Override
    public void stop() {
        try {
            if (server != null) server.close();
            if (connection != null) connection.close();
            if (worker != null) {
                worker.shutdownNow();
                if (!worker.awaitTermination(5, TimeUnit.SECONDS)) {
                    throw new IllegalStateException("Core fixture worker did not stop");
                }
            }
        } catch (Exception failure) {
            throw new IllegalStateException("Cannot close Core fixture", failure);
        } finally {
            if (reactiveUrlOverridden) {
                if (previousReactiveUrl == null) System.clearProperty(REACTIVE_URL_PROPERTY);
                else System.setProperty(REACTIVE_URL_PROPERTY, previousReactiveUrl);
                reactiveUrlOverridden = false;
            }
            if (database != null) database.stop();
        }
    }
}
