/*
 * This file was last modified at 2026.04.05 22:27 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordSchedulerService.java
 * $Id$
 */

package su.svn.api.services.schedulers;

import io.quarkus.scheduler.Scheduled;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import su.svn.api.services.domain.RecordDataService;

import java.util.concurrent.atomic.AtomicBoolean;

@ApplicationScoped
public class RecordSchedulerService {

    public static final int SYNC_SIZE = 2000;
    private final AtomicBoolean done = new AtomicBoolean(true);
    private final AtomicBoolean fire = new AtomicBoolean(false);

    @Inject
    RecordDataService recordDataService;

    @Inject
    io.vertx.core.Vertx vertx;

    @Scheduled(every = "13s")
    void job() {
        if (this.fire.get() && this.done.compareAndSet(true, false)) {
            vertx.runOnContext(v -> {
                recordDataService.sync(0, SYNC_SIZE)
                        .subscribe().with(
                                result -> {},
                                Throwable::printStackTrace
                        );

                this.fire.set(false);
                this.done.set(true);
            });
        }
    }

    public void fire(boolean fire) {
        this.fire.set(fire);
    }
}
