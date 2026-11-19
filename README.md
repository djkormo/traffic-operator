kubebuilder init --domain djkormo.github.io --repo github.com/djkormo/traffic-operator --skip-go-version-check 

kubebuilder create api --group network --version v1alpha1 --kind TrafficCheck


