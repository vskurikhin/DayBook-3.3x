/*
 * This file was last modified at 2026.10.14 15:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordsPageTimeout.java
 * $Id$
 */

package su.svn.api.services.domain;

import io.quarkus.runtime.Startup;
import io.smallrye.mutiny.Uni;
import io.smallrye.mutiny.infrastructure.Infrastructure;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import org.eclipse.microprofile.config.inject.ConfigProperty;

import java.time.Duration;
import java.util.concurrent.ScheduledExecutorService;

/** The deadline applies to Core page reads only. The scheduler is framework-owned. */
@Startup
@ApplicationScoped
public class RecordsPageTimeout {
    public static final int MAX_TIMEOUT_MS = 60_000;
    public static final int MIN_TIMEOUT_MS = 10;
    private final Duration duration;
    private final ScheduledExecutorService scheduler;

    @Inject
    public RecordsPageTimeout(@ConfigProperty(name = "application.records-page.remote-timeout-ms", defaultValue = "500") int milliseconds) {
        this(milliseconds, Infrastructure.getDefaultWorkerPool());
    }

    RecordsPageTimeout(int milliseconds, ScheduledExecutorService scheduler) {
        if (milliseconds < MIN_TIMEOUT_MS || milliseconds > MAX_TIMEOUT_MS) {
            throw new IllegalArgumentException("application.records-page.remote-timeout-ms must be between 10 and 60000");
        }
        this.duration = Duration.ofMillis(milliseconds);
        this.scheduler = scheduler;
    }

    <T> Uni<T> apply(Uni<T> source) {
        return source.ifNoItem().after(duration).on(scheduler).fail();
    }
}
