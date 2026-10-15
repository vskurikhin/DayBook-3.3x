/*
 * This file was last modified at 2026.10.14 15:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * PageReadPolicy.java
 * $Id$
 */

package su.svn.api.services.domain;

import io.smallrye.mutiny.CompositeException;
import io.smallrye.mutiny.Uni;
import io.vertx.core.Context;
import io.vertx.core.Vertx;
import su.svn.api.repository.RemotePageFailure;

import java.util.concurrent.Executor;
import java.util.function.Supplier;

/** Per-subscription selection; Mutiny owns winner serialization and source cancellation. */
final class PageReadPolicy {
    private PageReadPolicy() {
    }

    /**
     * Races local and remote reads independently for each subscription.
     * Returns the first successful result, unless a non-recoverable remote failure
     * wins first. Recoverable remote failures allow the local read to continue.
     * Cancels the remaining source once a winner is selected.
     *
     * @param local supplies the local read
     * @param remote supplies the remote read
     * @param timeout bounds the remote read duration
     * @param <T> the result type
     * @return a lazy result; if both sources fail, an {@link IllegalStateException}
     *         with the local cause and the remote failure suppressed
     */
    static <T> Uni<T> read(Supplier<Uni<T>> local, Supplier<Uni<T>> remote, RecordsPageTimeout timeout) {
        return Uni.createFrom().deferred(() -> {
            Context owner = Vertx.currentContext();
            Executor context = command -> {
                if (owner == null || Vertx.currentContext() == owner) {
                    command.run();
                } else {
                    owner.runOnContext(ignored -> command.run());
                }
            };
            var localSource = createLocalRead(local, context);
            var remoteSource = createRemoteRead(remote, timeout, context);
            return Uni.join()
                    .first(localSource, remoteSource)
                    .withItem()
                    .onFailure(CompositeException.class)
                    .transform(PageReadPolicy::createSourcesExhaustedException)
                    .flatMap(outcome -> outcome.failure() == null
                            ? Uni.createFrom().item(outcome.value())
                            : Uni.createFrom().failure(outcome.failure()));
        });
    }

    private static <T> Uni<Outcome<T>> createLocalRead(Supplier<Uni<T>> local, Executor context) {
        return Uni.createFrom().<T>deferred(local::get)
                // A remote completion or a caller abort must close the local session on its owner.
                .onCancellation()
                .call(() -> Uni.createFrom().voidItem().emitOn(context))
                .emitOn(context)
                .onFailure()
                .transform(LocalFailure::new)
                .map(value -> new Outcome<T>(value, null));
    }

    private static <T> Uni<Outcome<T>> createRemoteRead(Supplier<Uni<T>> remote, RecordsPageTimeout timeout, Executor context) {
        return timeout.apply(Uni.createFrom().<T>deferred(remote::get))
                .emitOn(context).map(value -> new Outcome<T>(value, null))
                .onFailure(failure -> !RemotePageFailure.isRecoverable(failure))
                .recoverWithItem(failure -> new Outcome<>(null, failure));
    }

    private static IllegalStateException createSourcesExhaustedException(CompositeException failure) {
        var causes = failure.getCauses();
        // Join records arrival order; the diagnostic contract always names local first.
        var localFailure = causes.getFirst() instanceof LocalFailure ? causes.getFirst() : causes.get(1);
        var remoteFailure = causes.getFirst() == localFailure ? causes.get(1) : causes.getFirst();
        var exhausted = new IllegalStateException("Both records-page sources failed", localFailure.getCause());
        exhausted.addSuppressed(remoteFailure);
        return exhausted;
    }

    private static final class LocalFailure extends RuntimeException {
        private LocalFailure(Throwable cause) {
            super(null, cause, false, false);
        }
    }

    private record Outcome<T>(T value, Throwable failure) {
    }
}
