/*
 * This file was last modified at 2026.06.28 12:20 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * FireSchedulerService.java
 * $Id$
 */

package su.svn.api.services.schedulers;

import io.quarkus.scheduler.Scheduled;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;

@ApplicationScoped
public class FireSchedulerService {

    @Inject
    RecordSchedulerService schedulerService;

    @Scheduled(every = "17s")
    void job() {
        schedulerService.fire(true);
    }
}
