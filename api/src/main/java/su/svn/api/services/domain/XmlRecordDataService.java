/*
 * This file was last modified at 2026.05.22 18:49 by Victor N. Skurikhin.
 * This is free and unencumbered software released into the public domain.
 * For more information, please refer to <http://unlicense.org>
 * XmlRecordDataService.java
 * $Id$
 */

package su.svn.api.services.domain;

import io.smallrye.mutiny.Uni;
import jakarta.enterprise.context.ApplicationScoped;
import jakarta.inject.Inject;
import su.svn.api.models.dto.NewXmlRecord;
import su.svn.api.models.dto.ResourceXmlRecord;
import su.svn.api.models.dto.UpdateXmlRecord;
import su.svn.api.repository.PostRecordRepository;
import su.svn.api.repository.XmlRecordRepository;
import su.svn.api.services.mappers.XmlRecordMapper;

import java.util.UUID;

/**
 * Reactive application service for XML record operations.
 *
 * <p>
 * This service coordinates interactions between:
 * </p>
 * <ul>
 *     <li>{@link XmlRecordRepository} for remote XML persistence</li>
 *     <li>{@link PostRecordRepository} for local post synchronization</li>
 *     <li>{@link XmlRecordMapper} for DTO/entity transformations</li>
 * </ul>
 *
 * <p>
 * All operations are implemented using reactive Mutiny APIs
 * and return {@link Uni} instances.
 * </p>
 *
 * <h2>Responsibilities</h2>
 * <ul>
 *     <li>Create XML records</li>
 *     <li>Update XML records</li>
 *     <li>Delete XML records</li>
 *     <li>Synchronize local post entities</li>
 * </ul>
 *
 * @see XmlRecordRepository
 * @see PostRecordRepository
 * @see XmlRecordMapper
 * @see io.smallrye.mutiny.Uni
 */
@ApplicationScoped
public class XmlRecordDataService {

    @Inject
    XmlRecordRepository recordRepository;

    @Inject
    XmlRecordMapper mapper;

    @Inject
    PostRecordRepository postRecordRepository;

    /**
     * Deletes an XML record and disables the corresponding local post.
     *
     * <p>
     * Both operations are executed asynchronously in parallel.
     * </p>
     *
     * @param id XML record identifier
     * @return completion notification
     */
    public Uni<Void> delete(UUID id) {
        return Uni.combine().all().unis(
                recordRepository.delete(id),
                postRecordRepository.disable(id)
        ).withUni(l -> Uni.createFrom().voidItem());
    }

    /**
     * Creates a new XML record.
     *
     * @param newXmlRecord XML creation DTO
     * @return created XML resource
     */
    public Uni<ResourceXmlRecord> post(NewXmlRecord newXmlRecord) {
        return recordRepository.post(newXmlRecord);
    }

    /**
     * Updates an existing XML record and synchronizes
     * the corresponding local post entity.
     *
     * @param updateXmlRecord XML update DTO
     * @return updated XML resource
     */
    public Uni<ResourceXmlRecord> put(UpdateXmlRecord updateXmlRecord) {
        return recordRepository.put(updateXmlRecord)
                .flatMap(resourceJsonRecord ->
                        postRecordRepository.update(mapper.toEntity(updateXmlRecord))
                                .map(postRecord -> mapper.toResource(postRecord))
                );
    }
}