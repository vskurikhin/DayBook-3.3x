pipeline {
    agent any // Runs the pipeline on any available agent/node

    stages {
        stage('Build') {
            steps {
                sh 'cd auth ; touch .env ; make'

                // Ensure the gradlew script has executable permissions
                sh 'chmod +x gradlew'

                // Run the 'build' task using the Gradle wrapper
                sh '''
                    ./gradlew build -xtest -Dquarkus.package.jar.enabled=false -Dquarkus.native.enabled=true
                '''
            }
        }

        stage('Test') {
            steps {
                // Run the 'test' task specifically, if desired, though 'build' often includes it
                sh './gradlew test'
            }
            // Optional: Archive test results (e.g., JUnit format)
            post {
                always {
                    junit '**/build/test-results/test/*.xml'
                }
            }
        }

        // Add more stages like 'Deploy', 'Staging', etc., as needed
        stage('Staging artifact') {
            steps {
                script {
                    VERSION = sh(
                        script: './gradlew properties -q | grep "^version:" | awk \'{print $2}\'',
                        returnStdout: true
                    ).trim()
                }
                sh """
                    mkdir distributive-${VERSION}
                    mkdir distributive-${VERSION}/migrations
                    mkdir distributive-${VERSION}/migrations/api
                    mkdir distributive-${VERSION}/migrations/auth
                    mkdir distributive-${VERSION}/migrations/core
                    cp api/build/api-*-runner distributive-${VERSION}/
                    cp api/src/main/resources/db/changelog/Change-Sets/* distributive-${VERSION}/migrations/api/
                    cp auth/cmd/server/auth-server distributive-${VERSION}/
                    cp auth/cmd/server/cmd/migrations/* distributive-${VERSION}/migrations/auth/
                    cp core/build/libs/core-*.jar distributive-${VERSION}/
                    cp core/src/main/resources/db/changelog/Change-Sets/* distributive-${VERSION}/migrations/core/
                """
                sh "tar czvf distributive-${VERSION}.tar.gz distributive-${VERSION}"
                archiveArtifacts artifacts: "distributive-${VERSION}.tar.gz", fingerprint: true
            }
        }
    }
}