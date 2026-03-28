/*
 * This file was last modified at 2026.04.04 13:04 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * RecordSchedulerService.java
 * $Id$
 */

package su.svn.api.services.schedulers;

import io.quarkus.scheduler.Scheduled;
import jakarta.enterprise.context.ApplicationScoped;

import java.util.concurrent.atomic.AtomicBoolean;

@ApplicationScoped
public class RecordSchedulerService {

    private final AtomicBoolean done = new AtomicBoolean(true);
    private final AtomicBoolean fire = new AtomicBoolean(false);

    @Scheduled(every = "15s")
    void job() {
        if (this.fire.get() && this.done.compareAndSet(true, false)) {
            // TODO
            /*
                repository.persistAll(generate())
                .subscribe().with(
                    r -> {},
                    Throwable::printStackTrace
                );
             */
            this.fire.set(false);
            this.done.set(true);
        }
    }

    public void fire(boolean fire) {
        this.fire.set(fire);
    }
}
