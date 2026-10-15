package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import io.smallrye.mutiny.subscription.UniEmitter;
import jakarta.ws.rs.ForbiddenException;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

import java.net.UnknownHostException;
import java.util.concurrent.CompletionException;
import java.util.concurrent.ScheduledExecutorService;
import java.util.concurrent.ScheduledFuture;
import java.util.concurrent.TimeUnit;
import java.util.concurrent.atomic.AtomicInteger;
import java.util.concurrent.atomic.AtomicReference;

import static org.junit.jupiter.api.Assertions.*;
import static org.mockito.Mockito.*;

class PageReadPolicyTest {
    private ScheduledExecutorService scheduler;
    private ScheduledFuture<?> timer;
    private AtomicReference<Runnable> deadline;
    private RecordsPageTimeout timeout;

    @BeforeEach
    void setup() {
        scheduler = mock(ScheduledExecutorService.class);
        timer = mock(ScheduledFuture.class);
        deadline = new AtomicReference<>();
        doAnswer(call -> {
            deadline.set(call.getArgument(0));
            return timer;
        }).when(scheduler).schedule(any(Runnable.class), anyLong(), any(TimeUnit.class));
        timeout = new RecordsPageTimeout(1000, scheduler);
    }

    @Test
    void localSuccessCancelsRemoteAndTimerWithoutWaiting() {
        var local = new Source();
        var remote = new Source();
        var result = PageReadPolicy.read(local::uni, remote::uni, timeout).subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS);
        local.emitter.complete("local");
        assertEquals("local", result.join());
        assertEquals(1, remote.cancellations.get());
        verify(timer).cancel(false);
        remote.emitter.complete("late");
        deadline.get().run();
        assertEquals("local", result.join());
    }

    @Test
    void remoteSuccessCancelsLocalAndTimer() {
        var local = new Source();
        var remote = new Source();
        var result = PageReadPolicy.read(local::uni, remote::uni, timeout).subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS);
        remote.emitter.complete("remote");
        assertEquals("remote", result.join());
        assertEquals(1, local.cancellations.get());
        verify(timer).cancel(false);
    }

    @Test
    void remoteTimeoutRetainsPendingLocal() {
        var local = new Source();
        var remote = new Source();
        var result = PageReadPolicy.read(local::uni, remote::uni, timeout).subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS);
        deadline.get().run();
        assertFalse(result.isDone());
        assertEquals(0, local.cancellations.get());
        assertEquals(1, remote.cancellations.get());
        local.emitter.complete("local");
        assertEquals("local", result.join());
        verify(scheduler).schedule(any(Runnable.class), eq(1000L), eq(TimeUnit.MILLISECONDS));
    }

    @Test
    void localFailureThenRemoteTimeoutRetainsBothCauses() {
        var local = new Source();
        var remote = new Source();
        var result = PageReadPolicy.read(local::uni, remote::uni, timeout).subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS);
        var databaseFailure = new IllegalStateException("database unavailable");
        local.emitter.fail(databaseFailure);
        assertFalse(result.isDone());
        deadline.get().run();
        var failure = assertThrows(CompletionException.class, result::join).getCause();
        assertInstanceOf(IllegalStateException.class, failure);
        assertSame(databaseFailure, failure.getCause());
        assertEquals(1, failure.getSuppressed().length);
        assertInstanceOf(io.smallrye.mutiny.TimeoutException.class, failure.getSuppressed()[0]);
    }

    @Test
    void remoteFailureFirstStillReportsLocalAsCause() {
        var local = new Source();
        var remote = new Source();
        var result = PageReadPolicy.read(local::uni, remote::uni, timeout).subscribeAsCompletionStage()
                .orTimeout(5, TimeUnit.SECONDS);
        var dns = new UnknownHostException("core.invalid");
        var database = new IllegalStateException("db");
        remote.emitter.fail(dns);
        assertFalse(result.isDone());
        local.emitter.fail(database);
        var failure = assertThrows(CompletionException.class, result::join).getCause();
        assertSame(database, failure.getCause());
        assertEquals(1, failure.getSuppressed().length);
        assertSame(dns, failure.getSuppressed()[0]);
    }

    @Test
    void localFailureThenRemoteSuccess() {
        var local = new Source();
        var remote = new Source();
        var result = PageReadPolicy.read(local::uni, remote::uni, timeout).subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS);
        local.emitter.fail(new IllegalStateException("db"));
        assertFalse(result.isDone());
        remote.emitter.complete("remote");
        assertEquals("remote", result.join());
    }

    @Test
    void fatalRemoteCancelsLocalAndPreservesOriginalFailure() {
        var local = new Source();
        var remote = new Source();
        var result = PageReadPolicy.read(local::uni, remote::uni, timeout).subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS);
        var failure = new ForbiddenException();
        remote.emitter.fail(failure);
        assertSame(failure, assertThrows(CompletionException.class, result::join).getCause());
        assertEquals(1, local.cancellations.get());
    }

    @Test
    void outerCancellationStopsBothSourcesOnce() {
        var local = new Source();
        var remote = new Source();
        var subscription = PageReadPolicy.read(local::uni, remote::uni, timeout)
                .subscribe().with(ignored -> fail("cancelled request emitted"), ignored -> fail("cancelled request failed"));
        subscription.cancel();
        subscription.cancel();
        assertEquals(1, local.cancellations.get());
        assertEquals(1, remote.cancellations.get());
        verify(timer).cancel(false);
    }

    @Test
    void synchronousRemoteThrowDoesNotBypassPendingLocal() {
        var local = new Source();
        var result = PageReadPolicy.read(local::uni,
                () -> { throw new jakarta.ws.rs.ProcessingException(new UnknownHostException()); }, timeout)
                .subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS);
        assertFalse(result.isDone());
        local.emitter.complete("local");
        assertEquals("local", result.join());
    }

    @Test
    void synchronousLocalThrowStillAllowsRemote() {
        var result = PageReadPolicy.<String>read(() -> { throw new IllegalStateException("db"); },
                () -> Uni.createFrom().item("remote"), timeout).subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS);
        assertEquals("remote", result.join());
    }

    @Test
    void synchronousLocalSuccessDoesNotLeakRemoteSubscriptionOrTimer() {
        var remote = new Source();
        var result = PageReadPolicy.read(() -> Uni.createFrom().item("local"), remote::uni, timeout)
                .subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS);
        assertEquals("local", result.join());
        assertNull(remote.emitter);
        verifyNoInteractions(scheduler);
    }

    @Test
    void eachSubscriptionHasItsOwnSources() {
        var constructions = new AtomicInteger();
        var uni = PageReadPolicy.read(() -> Uni.createFrom().item(constructions.incrementAndGet()),
                () -> Uni.createFrom().nothing(), timeout);
        assertEquals(1, uni.subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS).join());
        assertEquals(2, uni.subscribeAsCompletionStage().orTimeout(5, java.util.concurrent.TimeUnit.SECONDS).join());
    }

    @Test
    void overlappingRequestsDoNotShareSelectionOrCancellation() {
        var firstLocal = new Source();
        var firstRemote = new Source();
        var secondLocal = new Source();
        var secondRemote = new Source();
        var first = PageReadPolicy.read(firstLocal::uni, firstRemote::uni, timeout)
                .subscribeAsCompletionStage().orTimeout(5, TimeUnit.SECONDS);
        var second = PageReadPolicy.read(secondLocal::uni, secondRemote::uni, timeout)
                .subscribeAsCompletionStage().orTimeout(5, TimeUnit.SECONDS);
        firstRemote.emitter.fail(new UnknownHostException());
        secondRemote.emitter.complete("second");
        assertEquals("second", second.join());
        assertEquals(1, secondLocal.cancellations.get());
        assertEquals(0, firstLocal.cancellations.get());
        assertFalse(first.isDone());
        firstLocal.emitter.complete("first");
        assertEquals("first", first.join());
    }

    @Test
    void timeoutBoundsAreValidated() {
        for (int invalid : new int[]{Integer.MIN_VALUE, -1, 0, 9, 60001, Integer.MAX_VALUE}) {
            assertThrows(IllegalArgumentException.class, () -> new RecordsPageTimeout(invalid, scheduler));
        }
        assertDoesNotThrow(() -> new RecordsPageTimeout(10, scheduler));
        assertDoesNotThrow(() -> new RecordsPageTimeout(60000, scheduler));
    }
/*

    @Test
    void remoteWinAndCallerCancellationCleanLocalOnOwnerContext1() throws Exception {
        var vertx = io.vertx.core.Vertx.vertx();
        try {
            for (String event : new String[]{"success", "abort", "fatal"}) {
                var setup = new java.util.concurrent.CompletableFuture<Runnable>();
                var cleaned = new java.util.concurrent.CompletableFuture<io.vertx.core.Context>();
                var owner = new java.util.concurrent.CompletableFuture<io.vertx.core.Context>();
                vertx.runOnContext(ignored -> {
                    owner.complete(io.vertx.core.Vertx.currentContext());
                    var remote = new Source();
                    var subscription = PageReadPolicy.read(
                            () -> Uni.createFrom().<String>nothing().onCancellation()
                                    .invoke(() -> cleaned.complete(io.vertx.core.Vertx.currentContext())),
                            remote::uni, timeout).subscribe().with(value -> {}, failure -> {});
                    setup.complete(switch (event) {
                        case "abort" -> subscription::cancel;
                        case "fatal" -> () -> remote.emitter.fail(new ForbiddenException());
                        default -> () -> remote.emitter.complete("remote");
                    });
                });
                setup.get(5, TimeUnit.SECONDS).run();
                assertSame(owner.get(5, TimeUnit.SECONDS), cleaned.get(5, TimeUnit.SECONDS));
            }
        } finally {
            vertx.close().toCompletionStage().toCompletableFuture().get(5, TimeUnit.SECONDS);
        }
    }
*/

    @Test
    void remoteWinAndCallerCancellationCleanLocalOnOwnerContext() throws Exception {
        var vertx = io.vertx.core.Vertx.vertx();
        try {
            for (String event : new String[]{"success", "abort", "fatal"}) {
                var setup = new java.util.concurrent.CompletableFuture<Runnable>();
                var cleaned = new java.util.concurrent.CompletableFuture<io.vertx.core.Context>();
                var owner = new java.util.concurrent.CompletableFuture<io.vertx.core.Context>();

                vertx.runOnContext(ignored -> {
                    owner.complete(io.vertx.core.Vertx.currentContext());
                    var remote = new Source();
                    var subscription = PageReadPolicy.read(
                            () -> Uni.createFrom().<String>nothing()
                                    .onCancellation()
                                    .invoke(() -> cleaned.complete(
                                            io.vertx.core.Vertx.currentContext())),
                            remote::uni,
                            timeout
                    ).subscribe().with(value -> {}, failure -> {});

                    Runnable trigger = switch (event) {
                        case "abort" -> subscription::cancel;
                        case "fatal" -> () -> remote.emitter.fail(new ForbiddenException());
                        default -> () -> remote.emitter.complete("remote");
                    };
                    setup.complete(trigger);
                });

                setup.get(5, TimeUnit.SECONDS).run();
                assertSame(
                        owner.get(5, TimeUnit.SECONDS),
                        cleaned.get(5, TimeUnit.SECONDS),
                        "Local cleanup must run on the owner context: " + event
                );
            }
        } finally {
            vertx.close().toCompletionStage().toCompletableFuture()
                    .get(5, TimeUnit.SECONDS);
        }
    }

    @Test
    void timeoutFailureContinuesOnOwnerContext() throws Exception {
        var vertx = io.vertx.core.Vertx.vertx();
        try {
            var ready = new java.util.concurrent.CompletableFuture<io.vertx.core.Context>();
            var completed = new java.util.concurrent.CompletableFuture<io.vertx.core.Context>();
            vertx.runOnContext(ignored -> {
                PageReadPolicy.<String>read(() -> Uni.createFrom().failure(new IllegalStateException("db")),
                        () -> Uni.createFrom().nothing(), timeout).subscribe()
                        .with(value -> completed.completeExceptionally(new AssertionError("unexpected item")),
                                failure -> completed.complete(io.vertx.core.Vertx.currentContext()));
                ready.complete(io.vertx.core.Vertx.currentContext());
            });
            var owner = ready.get(5, TimeUnit.SECONDS);
            deadline.get().run();
            assertSame(owner, completed.get(5, TimeUnit.SECONDS));
        } finally {
            vertx.close().toCompletionStage().toCompletableFuture().get(5, TimeUnit.SECONDS);
        }
    }

    private static final class Source {
        UniEmitter<? super String> emitter;
        AtomicInteger cancellations = new AtomicInteger();

        Uni<String> uni() {
            return Uni.createFrom().<String>emitter(value -> emitter = value)
                    .onCancellation().invoke(cancellations::incrementAndGet);
        }
    }
}
