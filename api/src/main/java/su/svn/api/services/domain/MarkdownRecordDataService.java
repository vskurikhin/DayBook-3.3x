/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * MarkdownRecordDataService.java
 * $Id$
 */

package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import su.svn.api.models.dto.NewMarkdownRecord;
import su.svn.api.models.dto.ResourceMarkdownRecord;
import su.svn.api.models.dto.UpdateMarkdownRecord;
import su.svn.api.repository.MarkdownRecordRepository;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.services.mappers.MarkdownRecordMapper;

import java.util.UUID;

/**
 * Reactive application service for markdown record operations.
 *
 * <p>
 * This service coordinates interactions between:
 * </p>
 * <ul>
 *     <li>{@link MarkdownRecordRepository} for remote markdown record persistence</li>
 *     <li>{@link PostRecordRepository} for local post record synchronization</li>
 *     <li>{@link MarkdownRecordMapper} for DTO and entity transformations</li>
 * </ul>
 *
 * <p>
 * All operations are implemented using Mutiny reactive programming primitives
 * and return {@link Uni} instances.
 * </p>
 *
 * <h2>Responsibilities</h2>
 * <ul>
 *     <li>Create markdown records</li>
 *     <li>Update markdown records</li>
 *     <li>Delete markdown records</li>
 *     <li>Synchronize markdown data with local post records</li>
 * </ul>
 *
 * <h2>Reactive Behavior</h2>
 * <p>
 * The service performs non-blocking asynchronous operations and combines
 * multiple reactive data sources where necessary.
 * </p>
 *
 * @see MarkdownRecordRepository
 * @see PostRecordRepository
 * @see MarkdownRecordMapper
 * @see io.smallrye.mutiny.Uni
 */
@ApplicationScoped
public class MarkdownRecordDataService {

    /**
     * Repository responsible for remote markdown record operations.
     */
    @Inject
    MarkdownRecordRepository recordRepository;

    /**
     * Mapper used for converting between DTOs and {@code PostRecord} entities.
     */
    @Inject
    MarkdownRecordMapper mapper;

    /**
     * Repository responsible for local post record persistence and synchronization.
     */
    @Inject
    PostRecordRepository postRecordRepository;

    /**
     * Deletes a markdown record by its identifier.
     *
     * <p>
     * This operation performs two asynchronous actions in parallel:
     * </p>
     * <ul>
     *     <li>Deletes the markdown record through the remote repository</li>
     *     <li>Disables the corresponding local post record</li>
     * </ul>
     *
     * <p>
     * The method completes successfully only when both operations finish.
     * </p>
     *
     * @param id the unique identifier of the markdown record
     * @return a {@link Uni} emitting completion notification
     */
    public Uni<Void> delete(UUID id) {
        return Uni.combine().all().unis(
                recordRepository.delete(id),
                postRecordRepository.disable(id)
        ).withUni(l -> Uni.createFrom().voidItem());
    }

    /**
     * Creates a new markdown record.
     *
     * <p>
     * The request is delegated to the remote markdown repository.
     * </p>
     *
     * @param newMarkdownRecord DTO containing data for the new markdown record
     * @return a {@link Uni} emitting the created markdown resource
     */
    public Uni<ResourceMarkdownRecord> post(NewMarkdownRecord newMarkdownRecord) {
        return recordRepository.post(newMarkdownRecord);
    }

    /**
     * Updates an existing markdown record.
     *
     * <p>
     * The update process consists of:
     * </p>
     * <ol>
     *     <li>Updating the remote markdown record</li>
     *     <li>Updating the corresponding local post record</li>
     *     <li>Mapping the updated entity back to a resource DTO</li>
     * </ol>
     *
     * <p>
     * The resulting resource reflects the synchronized local post state.
     * </p>
     *
     * @param updateMarkdownRecord DTO containing updated markdown data
     * @return a {@link Uni} emitting the updated markdown resource
     */
    public Uni<ResourceMarkdownRecord> put(UpdateMarkdownRecord updateMarkdownRecord) {
        return recordRepository.put(updateMarkdownRecord)
                .flatMap(resourceJsonRecord ->
                        postRecordRepository.update(mapper.toEntity(updateMarkdownRecord))
                                .map(postRecord -> mapper.toResource(postRecord))
                );
    }
}